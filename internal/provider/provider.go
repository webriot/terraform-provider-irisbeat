// SPDX-License-Identifier: AGPL-3.0-only
package provider

import (
	"context"
	"os"

	"github.com/Webriot/terraform-provider-irisbeat/internal/api"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type irisbeatProvider struct{ version string }

func New(version string) func() provider.Provider {
	return func() provider.Provider { return irisbeatProvider{version: version} }
}

func (p irisbeatProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "irisbeat"
	resp.Version = p.version
}

func (irisbeatProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{Attributes: map[string]schema.Attribute{
		"base_url": schema.StringAttribute{Optional: true, Description: "Self-hosted Irisbeat base URL. Defaults to IRISBEAT_BASE_URL."},
		"api_key":  schema.StringAttribute{Optional: true, Sensitive: true, Description: "Irisbeat Write API key. Defaults to IRISBEAT_API_KEY."},
	}}
}

func (irisbeatProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config struct {
		BaseURL types.String `tfsdk:"base_url"`
		APIKey  types.String `tfsdk:"api_key"`
	}
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Unknown Irisbeat base URL", "The installation URL must be known before planning Monitor changes.")
	}
	if config.APIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Unknown Irisbeat API key", "The Write API key must be known before planning Monitor changes.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	baseURL, key := config.BaseURL.ValueString(), config.APIKey.ValueString()
	if config.BaseURL.IsNull() {
		baseURL = os.Getenv("IRISBEAT_BASE_URL")
	}
	if config.APIKey.IsNull() {
		key = os.Getenv("IRISBEAT_API_KEY")
	}
	client, err := api.New(baseURL, key)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Irisbeat provider configuration", err.Error())
		return
	}
	resp.ResourceData = client
}

func (irisbeatProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		func() resource.Resource { return &monitorResource{} },
		func() resource.Resource { return &destinationResource{} },
	}
}

func (irisbeatProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }
