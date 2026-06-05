package provider

import (
	"context"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &OpenShiftProvider{}

// OpenShiftProvider implements the Terraform provider for airgapped OpenShift deployments.
type OpenShiftProvider struct {
	version string
}

// OpenShiftProviderModel holds the provider-level configuration values.
type OpenShiftProviderModel struct {
	Kubeconfig           types.String `tfsdk:"kubeconfig"`
	InstallBinary        types.String `tfsdk:"install_binary"`
	OcBinary             types.String `tfsdk:"oc_binary"`
	PullSecretFile       types.String `tfsdk:"pull_secret_file"`
	SSHKey               types.String `tfsdk:"ssh_key"`
	AssistedServiceURL   types.String `tfsdk:"assisted_service_url"`
	AssistedServiceToken types.String `tfsdk:"assisted_service_token"`
	AssistedOfflineToken types.String `tfsdk:"assisted_offline_token"`
}

// ProviderData is passed to resources and data sources via ConfigureProvider.
type ProviderData struct {
	Kubeconfig           string
	InstallBinary        string
	OcBinary             string
	PullSecretFile       string
	SSHKey               string
	Version              string
	AssistedServiceURL   string
	AssistedServiceToken string
	AssistedTokenManager *TokenManager // non-nil when offline_token is set
}

// New returns a function that creates a new OpenShiftProvider.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &OpenShiftProvider{version: version}
	}
}

func (p *OpenShiftProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "openshift"
	resp.Version = p.version
}

func (p *OpenShiftProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Provider for airgapped on-premises OpenShift deployments on bare metal.",
		Attributes: map[string]schema.Attribute{
			"kubeconfig": schema.StringAttribute{
				Optional:    true,
				Description: "Path to kubeconfig file. Defaults to KUBECONFIG env var or ~/.kube/config.",
			},
			"install_binary": schema.StringAttribute{
				Optional:    true,
				Description: "Path to openshift-install binary. Defaults to 'openshift-install' on PATH.",
			},
			"oc_binary": schema.StringAttribute{
				Optional:    true,
				Description: "Path to oc binary. Defaults to 'oc' on PATH.",
			},
			"pull_secret_file": schema.StringAttribute{
				Optional:    true,
				Description: "Path to pull-secret.json file.",
			},
			"ssh_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "SSH public key string for cluster nodes.",
			},
			"assisted_service_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the Assisted Installer service, e.g. https://assisted-service.example.com.",
			},
			"assisted_service_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Bearer token for the Assisted Installer API. Mutually exclusive with assisted_offline_token.",
			},
			"assisted_offline_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Red Hat offline token (from console.redhat.com/openshift/token). Automatically exchanged for a bearer token and refreshed when it expires. Mutually exclusive with assisted_service_token.",
			},
		},
	}
}

func (p *OpenShiftProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config OpenShiftProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data := &ProviderData{Version: p.version}

	// kubeconfig
	if !config.Kubeconfig.IsNull() && !config.Kubeconfig.IsUnknown() {
		data.Kubeconfig = config.Kubeconfig.ValueString()
	} else if kc := os.Getenv("KUBECONFIG"); kc != "" {
		data.Kubeconfig = kc
	} else {
		home, err := os.UserHomeDir()
		if err == nil {
			data.Kubeconfig = filepath.Join(home, ".kube", "config")
		}
	}

	// install_binary
	if !config.InstallBinary.IsNull() && !config.InstallBinary.IsUnknown() {
		data.InstallBinary = config.InstallBinary.ValueString()
	} else {
		data.InstallBinary = "openshift-install"
	}

	// oc_binary
	if !config.OcBinary.IsNull() && !config.OcBinary.IsUnknown() {
		data.OcBinary = config.OcBinary.ValueString()
	} else {
		data.OcBinary = "oc"
	}

	// pull_secret_file
	if !config.PullSecretFile.IsNull() && !config.PullSecretFile.IsUnknown() {
		data.PullSecretFile = config.PullSecretFile.ValueString()
	}

	// ssh_key
	if !config.SSHKey.IsNull() && !config.SSHKey.IsUnknown() {
		data.SSHKey = config.SSHKey.ValueString()
	}

	// assisted_service_url
	if !config.AssistedServiceURL.IsNull() && !config.AssistedServiceURL.IsUnknown() {
		data.AssistedServiceURL = config.AssistedServiceURL.ValueString()
	}

	// assisted_service_token / assisted_offline_token (mutually exclusive)
	if !config.AssistedServiceToken.IsNull() && !config.AssistedServiceToken.IsUnknown() {
		data.AssistedServiceToken = config.AssistedServiceToken.ValueString()
	} else if !config.AssistedOfflineToken.IsNull() && !config.AssistedOfflineToken.IsUnknown() {
		data.AssistedTokenManager = NewTokenManager(config.AssistedOfflineToken.ValueString())
	}

	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *OpenShiftProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewInstallConfigResource,
		NewClusterResource,
		NewMirrorRegistryResource,
		NewImageMirrorResource,
		NewCatalogSourceResource,
		NewSubscriptionResource,
		NewMachineConfigResource,
		NewMachineSetResource,
	}
}

func (p *OpenShiftProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCompatibilityDataSource,
	}
}

// Version returns the provider version string (set at build time via main.go).
func (p *OpenShiftProvider) Version() string {
	return p.version
}
