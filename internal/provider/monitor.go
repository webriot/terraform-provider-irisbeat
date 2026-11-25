// SPDX-License-Identifier: AGPL-3.0-only
package provider

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/Webriot/terraform-provider-irisbeat/internal/api"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	whatwg "github.com/nlnwa/whatwg-url/url"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type monitorResource struct{ client *api.Client }

type monitorModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Kind              types.String `tfsdk:"kind"`
	URL               types.String `tfsdk:"url"`
	Hostname          types.String `tfsdk:"hostname"`
	Port              types.Int64  `tfsdk:"port"`
	Published         types.Bool   `tfsdk:"published"`
	ScheduleMode      types.String `tfsdk:"schedule_mode"`
	IntervalSeconds   types.Int64  `tfsdk:"interval_seconds"`
	TimeoutSeconds    types.Int64  `tfsdk:"timeout_seconds"`
	FailureThreshold  types.Int64  `tfsdk:"failure_threshold"`
	RecoveryThreshold types.Int64  `tfsdk:"recovery_threshold"`
	Cron              types.String `tfsdk:"cron"`
	Timezone          types.String `tfsdk:"timezone"`
	DestinationIDs    types.Set    `tfsdk:"destination_ids"`
}

func (r *monitorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_monitor"
}

func (r *monitorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manages an Irisbeat HTTP or TCP Monitor.", Attributes: map[string]schema.Attribute{
		"id":                 schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Opaque Irisbeat Monitor ID."},
		"name":               schema.StringAttribute{Required: true, Description: "Monitor name."},
		"kind":               schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Monitor kind. Use http or tcp."},
		"url":                schema.StringAttribute{Optional: true, Description: "HTTP or HTTPS check target. Required for HTTP Monitors."},
		"hostname":           schema.StringAttribute{Optional: true, Description: "DNS name or IP address. Required for TCP Monitors."},
		"port":               schema.Int64Attribute{Optional: true, Description: "Port from 1 to 65535. Required for TCP Monitors."},
		"published":          schema.BoolAttribute{Required: true, Description: "Whether the Monitor appears on the public status page."},
		"schedule_mode":      schema.StringAttribute{Optional: true, Computed: true, Description: "interval or calendar. New Monitors default to interval."},
		"interval_seconds":   schema.Int64Attribute{Optional: true, Computed: true, Description: "Outbound check interval, retained when scheduling in Calendar mode."},
		"timeout_seconds":    schema.Int64Attribute{Optional: true, Computed: true, Description: "Check timeout in seconds."},
		"failure_threshold":  schema.Int64Attribute{Optional: true, Computed: true, Description: "Consecutive failures required to open an Incident."},
		"recovery_threshold": schema.Int64Attribute{Optional: true, Computed: true, Description: "Consecutive successes required to resolve an Incident."},
		"cron":               schema.StringAttribute{Optional: true, Computed: true, Description: "Numeric five-field cron expression for Calendar scheduling."},
		"timezone":           schema.StringAttribute{Optional: true, Computed: true, Description: "Named Calendar timezone. Defaults to UTC when entering Calendar mode."},
		"destination_ids":    schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Description: "Complete set of Notification destination IDs. Omit to preserve saved routing or use an empty set to clear it."},
	}}
}

func (r *monitorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *monitorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config monitorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	switch config.Kind.ValueString() {
	case "http":
		if config.URL.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("url"), "Missing HTTP target", "HTTP Monitors require url.")
		}
		if !config.Hostname.IsNull() && !config.Hostname.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("hostname"), "Unexpected HTTP target attribute", "hostname is only available for TCP Monitors.")
		}
		if !config.Port.IsNull() && !config.Port.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("port"), "Unexpected HTTP target attribute", "port is only available for TCP Monitors.")
		}
	case "tcp":
		if config.Hostname.IsNull() || (!config.Hostname.IsUnknown() && config.Hostname.ValueString() == "") {
			resp.Diagnostics.AddAttributeError(path.Root("hostname"), "Missing TCP target", "TCP Monitors require hostname.")
		}
		if config.Port.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("port"), "Missing TCP port", "TCP Monitors require port.")
		} else if !config.Port.IsUnknown() && (config.Port.ValueInt64() < 1 || config.Port.ValueInt64() > 65535) {
			resp.Diagnostics.AddAttributeError(path.Root("port"), "Invalid TCP port", "Use an integer from 1 to 65535.")
		}
		if !config.URL.IsNull() && !config.URL.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("url"), "Unexpected TCP target attribute", "url is only available for HTTP Monitors.")
		}
	default:
		if !config.Kind.IsNull() && !config.Kind.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("kind"), "Unsupported Monitor kind", "Use http or tcp for the Monitor kind.")
		}
	}
	if config.ScheduleMode.IsUnknown() {
		return
	}
	switch config.ScheduleMode.ValueString() {
	case "calendar":
		if config.Cron.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("cron"), "Missing Calendar schedule", "Configure cron when schedule_mode is calendar.")
		}
	case "", "interval":
		if !config.Cron.IsNull() || !config.Timezone.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("schedule_mode"), "Calendar mode required", "Set schedule_mode to calendar to configure cron or timezone.")
		}
		if !config.ScheduleMode.IsNull() && config.ScheduleMode.ValueString() == "" {
			resp.Diagnostics.AddAttributeError(path.Root("schedule_mode"), "Invalid Monitor schedule", "Use interval or calendar.")
		}
	default:
		resp.Diagnostics.AddAttributeError(path.Root("schedule_mode"), "Invalid Monitor schedule", "Use interval or calendar.")
	}
}

// An import has no configuration, so it records the server's canonical spelling.
// Keep that state when every configured field already describes the saved Monitor.
func (r *monitorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var config, prior types.Object
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	savedFields := prior.Attributes()
	for name, desired := range config.Attributes() {
		saved := savedFields[name]
		if desired.IsNull() || desired.Equal(saved) {
			continue
		}
		var normalize func(string) string
		switch name {
		case "name":
			normalize = trimJS
		case "url":
			normalize = normalizeURL
		case "hostname":
			normalize = normalizeHostname
		case "cron":
			normalize = normalizeCron
		default:
			return
		}
		if desired.IsUnknown() || saved.IsNull() || saved.IsUnknown() ||
			normalize(desired.(types.String).ValueString()) != normalize(saved.(types.String).ValueString()) {
			return
		}
	}
	resp.Plan.Raw = req.State.Raw
}

func (r *monitorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan monitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fields := monitorFields(ctx, plan, monitorModel{}, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	monitor, err := r.client.CreateMonitor(ctx, fields)
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Irisbeat Monitor", err.Error())
		return
	}
	if state, ok := monitorState(ctx, monitor, plan, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func (r *monitorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var prior monitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	monitor, err := r.client.ReadMonitor(ctx, prior.ID.ValueString())
	if errors.Is(err, api.ErrMonitorMissing) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to refresh Irisbeat Monitor", err.Error())
		return
	}
	if state, ok := monitorState(ctx, monitor, prior, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func (r *monitorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior monitorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	patch := monitorFields(ctx, plan, prior, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id := prior.ID.ValueString()
	var monitor api.Monitor
	var err error
	if len(patch) == 0 {
		monitor, err = r.client.ReadMonitor(ctx, id)
	} else {
		monitor, err = r.client.UpdateMonitor(ctx, id, patch)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to update Irisbeat Monitor", err.Error())
		return
	}
	if state, ok := monitorState(ctx, monitor, plan, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func (r *monitorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var prior monitorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteMonitor(ctx, prior.ID.ValueString())
	if err != nil && !errors.Is(err, api.ErrMonitorMissing) {
		resp.Diagnostics.AddError("Unable to delete Irisbeat Monitor", err.Error())
	}
}

func (r *monitorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	monitor, err := r.client.ReadMonitor(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import Irisbeat Monitor", err.Error())
		return
	}
	if state, ok := monitorState(ctx, monitor, monitorModel{}, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func monitorFields(ctx context.Context, desired, prior monitorModel, diags *diag.Diagnostics) map[string]any {
	fields := make(map[string]any)
	for _, field := range []struct {
		key        string
		value, old types.String
	}{
		{"name", desired.Name, prior.Name},
		{"kind", desired.Kind, prior.Kind},
		{"url", desired.URL, prior.URL},
		{"hostname", desired.Hostname, prior.Hostname},
		{"scheduleMode", desired.ScheduleMode, prior.ScheduleMode},
		{"cron", desired.Cron, prior.Cron},
		{"timezone", desired.Timezone, prior.Timezone},
	} {
		if !field.value.IsUnknown() && !field.value.IsNull() && !field.value.Equal(field.old) {
			fields[field.key] = field.value.ValueString()
		}
	}
	if !desired.Published.IsUnknown() && !desired.Published.IsNull() && !desired.Published.Equal(prior.Published) {
		fields["published"] = desired.Published.ValueBool()
	}
	for _, field := range []struct {
		key        string
		value, old types.Int64
	}{
		{"port", desired.Port, prior.Port},
		{"intervalSeconds", desired.IntervalSeconds, prior.IntervalSeconds},
		{"timeoutSeconds", desired.TimeoutSeconds, prior.TimeoutSeconds},
		{"failureThreshold", desired.FailureThreshold, prior.FailureThreshold},
		{"recoveryThreshold", desired.RecoveryThreshold, prior.RecoveryThreshold},
	} {
		if !field.value.IsUnknown() && !field.value.IsNull() && !field.value.Equal(field.old) {
			fields[field.key] = field.value.ValueInt64()
		}
	}
	// The framework leaves omitted computed values unknown on a write.
	if !desired.DestinationIDs.IsNull() && !desired.DestinationIDs.IsUnknown() {
		ids := make([]string, 0, len(desired.DestinationIDs.Elements()))
		diags.Append(desired.DestinationIDs.ElementsAs(ctx, &ids, false)...)
		fields["destinationIds"] = ids
	}
	return fields
}

func monitorState(ctx context.Context, m api.Monitor, reference monitorModel, diags *diag.Diagnostics) (monitorModel, bool) {
	if m.Kind != "http" && m.Kind != "tcp" {
		diags.AddError("Unsupported Irisbeat Monitor kind", "This provider manages HTTP and TCP Monitors.")
		return monitorModel{}, false
	}
	destinationIDs, setDiags := types.SetValueFrom(ctx, types.StringType, m.DestinationIDs)
	diags.Append(setDiags...)
	state := monitorModel{
		ID: types.StringValue(m.ID), Name: equivalentString(m.Name, reference.Name, trimJS),
		Kind:      types.StringValue(m.Kind),
		Published: types.BoolValue(*m.Published), ScheduleMode: types.StringValue(m.ScheduleMode),
		IntervalSeconds: types.Int64Value(m.IntervalSeconds), TimeoutSeconds: types.Int64Value(m.TimeoutSeconds),
		FailureThreshold: types.Int64Value(m.FailureThreshold), RecoveryThreshold: types.Int64Value(m.RecoveryThreshold),
		DestinationIDs: destinationIDs,
	}
	if m.Kind == "tcp" {
		state.Hostname = equivalentString(m.Hostname, reference.Hostname, normalizeHostname)
		state.Port = types.Int64Value(m.Port)
	} else {
		state.URL = equivalentString(m.URL, reference.URL, normalizeURL)
	}
	if m.ScheduleMode == "calendar" {
		state.Cron = equivalentString(m.Cron, reference.Cron, normalizeCron)
		state.Timezone = types.StringValue(m.Timezone)
	}
	return state, !diags.HasError()
}

func equivalentString(saved string, reference types.String, normalize func(string) string) types.String {
	// Keep the planned spelling when Irisbeat saved an equivalent value.
	if !reference.IsNull() && !reference.IsUnknown() && normalize(reference.ValueString()) == saved {
		return reference
	}
	return types.StringValue(saved)
}

func normalizeCron(value string) string {
	return strings.Join(strings.FieldsFunc(value, jsWhitespace), " ")
}

func trimJS(value string) string { return strings.TrimFunc(value, jsWhitespace) }

func jsWhitespace(r rune) bool {
	return strings.ContainsRune(" \t\n\r\v\f\u00a0\u1680\u2028\u2029\u202f\u205f\u3000\ufeff", r) ||
		(r >= '\u2000' && r <= '\u200a')
}

func normalizeURL(value string) string {
	parsed, err := whatwg.Parse(value)
	if err != nil {
		return value
	}
	return parsed.Href(true)
}

var ipv6Literal = regexp.MustCompile(`^[0-9a-f:.]+$`)

func normalizeHostname(value string) string {
	// Irisbeat uses ECMAScript full, locale-independent Unicode lowercasing.
	host := cases.Lower(language.Und).String(value)
	if !strings.Contains(host, ":") {
		return host
	}
	literal := host
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		literal = host[1 : len(host)-1]
	}
	if ipv6Literal.MatchString(literal) {
		if parsed, err := whatwg.Parse("http://[" + literal + "]/"); err == nil {
			return strings.Trim(parsed.Hostname(), "[]")
		}
	}
	return value
}
