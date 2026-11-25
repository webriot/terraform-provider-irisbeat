// SPDX-License-Identifier: AGPL-3.0-only
package provider

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/Webriot/terraform-provider-irisbeat/internal/api"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	whatwg "github.com/nlnwa/whatwg-url/url"
)

type destinationResource struct{ client *api.Client }

type destinationModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Channel    types.String `tfsdk:"channel"`
	WebhookURL types.String `tfsdk:"webhook_url"`
}

func (r *destinationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_notification_destination"
}

func (r *destinationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manages an Irisbeat webhook Notification destination.", Attributes: map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, Description: "Opaque Irisbeat Notification destination ID."},
		"name":        schema.StringAttribute{Optional: true, Computed: true, Description: "Destination name. Irisbeat generates a name when omitted on create."},
		"channel":     schema.StringAttribute{Required: true, Description: "Notification channel. Use webhook."},
		"webhook_url": schema.StringAttribute{Required: true, Sensitive: true, Description: "HTTPS webhook URL. Changing the saved URL replaces the destination."},
	}}
}

func (r *destinationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*api.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid Irisbeat resource configuration", "The provider did not configure an Irisbeat API client.")
		return
	}
	r.client = client
}

func (r *destinationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config destinationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !config.Channel.IsNull() && !config.Channel.IsUnknown() && config.Channel.ValueString() != "webhook" {
		resp.Diagnostics.AddAttributeError(path.Root("channel"), "Unsupported Notification channel", "Use webhook for the Notification destination channel.")
	}
}

func (r *destinationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan destinationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fields := map[string]any{"channel": plan.Channel.ValueString(), "url": plan.WebhookURL.ValueString()}
	if !plan.Name.IsNull() && !plan.Name.IsUnknown() {
		fields["name"] = plan.Name.ValueString()
	}
	saved, err := r.client.CreateDestination(ctx, fields)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Irisbeat Notification destination", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, destinationState(saved, plan))...)
}

func (r *destinationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior destinationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	saved, err := r.client.ReadDestination(ctx, prior.ID.ValueString())
	if errors.Is(err, api.ErrDestinationMissing) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to refresh Irisbeat Notification destination", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, destinationState(saved, prior))...)
}

func (r *destinationResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, prior destinationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	// A saved Discord URL can exceed the input limit after normalization.
	if !plan.WebhookURL.IsUnknown() && !plan.WebhookURL.Equal(prior.WebhookURL) {
		value := plan.WebhookURL.ValueString()
		if _, valid := parseWebhookURL(value); !valid || utf16Length(value) > 2048 {
			// Attribute diagnostics may display the URL literal in a source excerpt.
			resp.Diagnostics.AddError("Invalid Irisbeat webhook URL", "Use an HTTPS webhook URL of at most 2048 characters without whitespace, embedded credentials, or a fragment.")
			return
		}
	}
	if req.State.Raw.IsNull() {
		return
	}
	var config destinationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Channel.Equal(prior.Channel) {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("channel"))
		return
	}
	if plan.WebhookURL.IsUnknown() {
		// Both CLIs can refine an update to a no-op, but reject an unplanned replacement.
		resp.Diagnostics.AddWarning("Webhook URL is unknown", "Apply the upstream dependency first and run a fresh plan to review any destination replacement. If you proceed with this plan and the saved URL would change, the CLI stops at this destination. Upstream changes may already have applied.")
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), prior.ID)...)
		return
	}
	if normalizeWebhookURL(plan.WebhookURL.ValueString()) != normalizeWebhookURL(prior.WebhookURL.ValueString()) {
		resp.Diagnostics.AddWarning("Webhook URL requires replacement", "If the CLI stops because the URL changed during apply, run a fresh plan and review the destination replacement before applying it.")
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("webhook_url"))
		return
	}
	if config.Name.IsNull() || (!plan.Name.IsUnknown() &&
		trimJS(plan.Name.ValueString()) == trimJS(prior.Name.ValueString())) {
		// Keep the imported spelling if the configured fields describe the saved destination.
		resp.Plan.Raw = req.State.Raw
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), prior.ID)...)
}

func (r *destinationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior destinationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	saved, err := r.client.RenameDestination(ctx, prior.ID.ValueString(), plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to rename Irisbeat Notification destination", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, destinationState(saved, plan))...)
}

func (r *destinationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	saved, err := r.client.ReadDestination(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import Irisbeat Notification destination", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, destinationState(saved, destinationModel{}))...)
}

func (r *destinationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var prior destinationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteDestination(ctx, prior.ID.ValueString())
	if err != nil && !errors.Is(err, api.ErrDestinationMissing) {
		resp.Diagnostics.AddError("Unable to delete Irisbeat Notification destination", err.Error())
	}
}

func destinationState(saved api.Destination, reference destinationModel) destinationModel {
	return destinationModel{
		ID: types.StringValue(saved.ID), Channel: types.StringValue(saved.Channel),
		Name:       equivalentString(saved.Name, reference.Name, trimJS),
		WebhookURL: equivalentString(saved.URL, reference.WebhookURL, normalizeWebhookURL),
	}
}

// Irisbeat preserves the input spelling for URLs outside its Discord webhook rules.
var discordWebhookPath = regexp.MustCompile(`^(/api(?:/v[0-9]+)?/webhooks/[^/]+/[^/]+)(?:/slack)?/?$`)

func normalizeWebhookURL(value string) string {
	parsed, valid := parseWebhookURL(value)
	if !valid {
		return value
	}
	host := parsed.Hostname()
	if strings.HasPrefix(host, "canary.") || strings.HasPrefix(host, "ptb.") {
		_, host, _ = strings.Cut(host, ".")
	}
	if host != "discord.com" && host != "discordapp.com" {
		return value
	}
	path := discordWebhookPath.FindStringSubmatch(parsed.Pathname())
	if path == nil {
		return value
	}
	parsed.SetPathname(path[1] + "/slack")
	return parsed.Href(false)
}

func parseWebhookURL(value string) (*whatwg.Url, bool) {
	if strings.IndexFunc(value, jsWhitespace) >= 0 {
		return nil, false
	}
	parsed, err := whatwg.Parse(value)
	return parsed, err == nil && parsed.Scheme() == "https" && parsed.Username() == "" && parsed.Password() == "" && parsed.Fragment() == ""
}

func utf16Length(value string) int {
	length := 0
	for _, r := range value {
		length += utf16.RuneLen(r)
	}
	return length
}
