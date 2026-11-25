// SPDX-License-Identifier: AGPL-3.0-only
package provider

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/Webriot/terraform-provider-irisbeat/internal/api"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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

	HeartbeatPath          types.String `tfsdk:"heartbeat_path"`
	SilenceSeconds         types.Int64  `tfsdk:"silence_seconds"`
	RecoverySpacingSeconds types.Int64  `tfsdk:"recovery_spacing_seconds"`
	GraceSeconds           types.Int64  `tfsdk:"grace_seconds"`
}

func (r *monitorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_monitor"
}

func (r *monitorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{Description: "Manages an Irisbeat Monitor.", Attributes: map[string]schema.Attribute{
		"id":                 schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Opaque Irisbeat Monitor ID."},
		"name":               schema.StringAttribute{Required: true, Description: "Monitor name."},
		"kind":               schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Description: "Monitor kind. Use http, tcp, or heartbeat."},
		"url":                schema.StringAttribute{Optional: true, Description: "HTTP or HTTPS check target. Required for HTTP Monitors."},
		"hostname":           schema.StringAttribute{Optional: true, Description: "DNS name or IP address. Required for TCP Monitors."},
		"port":               schema.Int64Attribute{Optional: true, Description: "Port from 1 to 65535. Required for TCP Monitors."},
		"published":          schema.BoolAttribute{Required: true, Description: "Whether the Monitor appears on the public status page."},
		"schedule_mode":      schema.StringAttribute{Optional: true, Computed: true, Description: "interval or calendar. New Monitors default to interval."},
		"interval_seconds":   schema.Int64Attribute{Optional: true, Computed: true, Description: "Check interval or expected Heartbeat receipt interval in seconds. HTTP and TCP Monitors retain it in Calendar mode."},
		"timeout_seconds":    schema.Int64Attribute{Optional: true, Computed: true, Description: "Check timeout in seconds."},
		"failure_threshold":  schema.Int64Attribute{Optional: true, Computed: true, Description: "Consecutive failures required to open an Incident."},
		"recovery_threshold": schema.Int64Attribute{Optional: true, Computed: true, Description: "Consecutive successes required to resolve an Incident."},
		"cron":               schema.StringAttribute{Optional: true, Computed: true, Description: "Numeric five-field cron expression for Calendar scheduling."},
		"timezone":           schema.StringAttribute{Optional: true, Computed: true, Description: "Named Calendar timezone. Defaults to UTC when entering Calendar mode."},

		"heartbeat_path":           schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Heartbeat receipt path for the saved Monitor ID. Authenticate receipts with the installation secret."},
		"silence_seconds":          schema.Int64Attribute{Optional: true, Computed: true, Description: "Seconds without a receipt before an Interval Heartbeat opens an Incident."},
		"recovery_spacing_seconds": schema.Int64Attribute{Optional: true, Computed: true, Description: "Minimum seconds between qualifying Interval Heartbeat recovery receipts."},
		"grace_seconds":            schema.Int64Attribute{Optional: true, Computed: true, Description: "Seconds after a scheduled Calendar Heartbeat occurrence to accept a receipt."},
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
	validateMonitorConfig(config, &resp.Diagnostics)
}

func configured(value attr.Value) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func validateMonitorConfig(config monitorModel, diags *diag.Diagnostics) {
	kind := config.Kind.ValueString()
	switch kind {
	case "http":
		validateHTTPTarget(config.URL, diags)
	case "tcp":
		if config.Hostname.IsNull() || (configured(config.Hostname) && config.Hostname.ValueString() == "") {
			diags.AddAttributeError(path.Root("hostname"), "Missing TCP target", "TCP Monitors require hostname.")
		}
		if config.Port.IsNull() {
			diags.AddAttributeError(path.Root("port"), "Missing TCP port", "TCP Monitors require port.")
		} else if configured(config.Port) && (config.Port.ValueInt64() < 1 || config.Port.ValueInt64() > 65535) {
			diags.AddAttributeError(path.Root("port"), "Invalid TCP port", "Use an integer from 1 to 65535.")
		}
	case "heartbeat":
		if configured(config.TimeoutSeconds) || configured(config.FailureThreshold) {
			diags.AddAttributeError(path.Root("kind"), "Outbound Monitor kind required", "Configure timeout_seconds or failure_threshold only for HTTP or TCP Monitors.")
		}
	default:
		if configured(config.Kind) {
			diags.AddAttributeError(path.Root("kind"), "Unsupported Monitor kind", "Use http, tcp, or heartbeat for the Monitor kind.")
		}
	}
	if kind == "tcp" || kind == "heartbeat" {
		if configured(config.URL) {
			diags.AddAttributeError(path.Root("url"), "HTTP kind required", "url is only available for HTTP Monitors.")
		}
	}
	if kind == "http" || kind == "heartbeat" {
		if configured(config.Hostname) {
			diags.AddAttributeError(path.Root("hostname"), "TCP kind required", "hostname is only available for TCP Monitors.")
		}
		if configured(config.Port) {
			diags.AddAttributeError(path.Root("port"), "TCP kind required", "port is only available for TCP Monitors.")
		}
	}
	if kind == "http" || kind == "tcp" {
		if configured(config.SilenceSeconds) || configured(config.RecoverySpacingSeconds) || configured(config.GraceSeconds) {
			diags.AddAttributeError(path.Root("kind"), "Heartbeat kind required", "Configure silence_seconds, recovery_spacing_seconds, or grace_seconds only for Heartbeat Monitors.")
		}
	}
	if config.ScheduleMode.IsUnknown() {
		return
	}
	switch config.ScheduleMode.ValueString() {
	case "calendar":
		if config.Kind.ValueString() == "heartbeat" &&
			(configured(config.IntervalSeconds) || configured(config.SilenceSeconds) || configured(config.RecoverySpacingSeconds)) {
			diags.AddAttributeError(path.Root("schedule_mode"), "Interval mode required", "Configure interval_seconds, silence_seconds, or recovery_spacing_seconds only for Interval Heartbeats.")
		}
		if config.Cron.IsNull() {
			diags.AddAttributeError(path.Root("cron"), "Missing Calendar schedule", "Configure cron when schedule_mode is calendar.")
		}
	case "", "interval":
		if configured(config.GraceSeconds) {
			diags.AddAttributeError(path.Root("grace_seconds"), "Calendar mode required", "Set schedule_mode to calendar to configure grace_seconds.")
		}
		if configured(config.Cron) || configured(config.Timezone) {
			diags.AddAttributeError(path.Root("schedule_mode"), "Calendar mode required", "Set schedule_mode to calendar to configure cron or timezone.")
		}
		if !config.ScheduleMode.IsNull() && config.ScheduleMode.ValueString() == "" {
			diags.AddAttributeError(path.Root("schedule_mode"), "Invalid Monitor schedule", "Use interval or calendar.")
		}
	default:
		diags.AddAttributeError(path.Root("schedule_mode"), "Invalid Monitor schedule", "Use interval or calendar.")
	}
}

func validateHTTPTarget(target types.String, diags *diag.Diagnostics) {
	if target.IsNull() {
		diags.AddAttributeError(path.Root("url"), "Missing HTTP target", "HTTP Monitors require url.")
		return
	}
	if target.IsUnknown() {
		return
	}
	value := target.ValueString()
	parsed, err := whatwg.Parse(value)
	if err != nil || (parsed.Scheme() != "http" && parsed.Scheme() != "https") ||
		parsed.Username() != "" || parsed.Password() != "" || len(utf16.Encode([]rune(value))) > 2048 {
		diags.AddAttributeError(path.Root("url"), "Invalid HTTP target",
			"Use an HTTP or HTTPS URL without embedded credentials and with at most 2,048 UTF-16 code units.")
	}
}

func (r *monitorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var config types.Object
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desiredFields := config.Attributes()
	// Configuration validation can run before expressions resolve during planning.
	if desiredFields["kind"].Equal(types.StringValue("http")) {
		validateHTTPTarget(desiredFields["url"].(types.String), &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if desiredFields["kind"].Equal(types.StringValue("heartbeat")) && desiredFields["schedule_mode"].IsNull() &&
		(configured(desiredFields["interval_seconds"]) || configured(desiredFields["silence_seconds"]) || configured(desiredFields["recovery_spacing_seconds"])) {
		interval := types.StringValue("interval")
		desiredFields["schedule_mode"] = interval
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("schedule_mode"), interval)...)
	}
	if req.State.Raw.IsNull() || resp.Diagnostics.HasError() {
		return
	}

	var prior types.Object
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	savedFields := prior.Attributes()
	if !desiredFields["kind"].Equal(savedFields["kind"]) {
		// The CLI can delete the old Monitor before Create validates resolved inputs.
		for name, desired := range desiredFields {
			if desired.IsUnknown() {
				resp.Diagnostics.AddAttributeError(path.Root(name), "Monitor replacement requires a known value",
					"Resolve this value before planning a Monitor kind change. Apply its upstream dependencies separately, then plan again.")
			}
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// An import records the server's canonical spelling. Keep it when every
	// configured field already describes the saved Monitor.
	for name, desired := range desiredFields {
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
	var config, plan monitorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateMonitorConfig(config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	monitor, err := r.client.CreateMonitor(ctx, monitorFields(plan, monitorModel{}))
	if err != nil {
		resp.Diagnostics.AddError("Unable to create Irisbeat Monitor", err.Error())
		return
	}
	if state, ok := monitorState(monitor, plan, &resp.Diagnostics); ok {
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
	if state, ok := monitorState(monitor, prior, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func (r *monitorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var config, plan, prior monitorModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateMonitorConfig(config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	patch := monitorFields(plan, prior)
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
	if state, ok := monitorState(monitor, plan, &resp.Diagnostics); ok {
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
	if state, ok := monitorState(monitor, monitorModel{}, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
	}
}

func monitorFields(desired, prior monitorModel) map[string]any {
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
		{"silenceSeconds", desired.SilenceSeconds, prior.SilenceSeconds},
		{"recoverySpacingSeconds", desired.RecoverySpacingSeconds, prior.RecoverySpacingSeconds},
		{"graceSeconds", desired.GraceSeconds, prior.GraceSeconds},
	} {
		if !field.value.IsUnknown() && !field.value.IsNull() && !field.value.Equal(field.old) {
			fields[field.key] = field.value.ValueInt64()
		}
	}
	return fields
}

func monitorState(m api.Monitor, reference monitorModel, diags *diag.Diagnostics) (monitorModel, bool) {
	if m.Kind != "http" && m.Kind != "tcp" && m.Kind != "heartbeat" {
		diags.AddError("Unsupported Irisbeat Monitor kind", "This provider manages HTTP, TCP, and Heartbeat Monitors.")
		return monitorModel{}, false
	}
	state := monitorModel{
		ID: types.StringValue(m.ID), Name: equivalentString(m.Name, reference.Name, trimJS),
		Kind:      types.StringValue(m.Kind),
		Published: types.BoolValue(*m.Published), ScheduleMode: types.StringValue(m.ScheduleMode),
		RecoveryThreshold: types.Int64Value(m.RecoveryThreshold),
	}
	if m.Kind == "heartbeat" {
		state.HeartbeatPath = types.StringValue("/heartbeat/" + url.PathEscape(m.ID))
		if m.ScheduleMode == "calendar" {
			state.GraceSeconds = types.Int64Value(m.GraceSeconds)
		} else {
			state.IntervalSeconds = types.Int64Value(m.IntervalSeconds)
			state.SilenceSeconds = types.Int64Value(m.SilenceSeconds)
			state.RecoverySpacingSeconds = types.Int64Value(m.RecoverySpacingSeconds)
		}
	} else {
		state.IntervalSeconds = types.Int64Value(m.IntervalSeconds)
		state.TimeoutSeconds = types.Int64Value(m.TimeoutSeconds)
		state.FailureThreshold = types.Int64Value(m.FailureThreshold)
		if m.Kind == "tcp" {
			state.Hostname = equivalentString(m.Hostname, reference.Hostname, normalizeHostname)
			state.Port = types.Int64Value(m.Port)
		} else {
			state.URL = equivalentString(m.URL, reference.URL, normalizeURL)
		}
	}
	if m.ScheduleMode == "calendar" {
		state.Cron = equivalentString(m.Cron, reference.Cron, normalizeCron)
		state.Timezone = types.StringValue(m.Timezone)
	}
	return state, true
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
