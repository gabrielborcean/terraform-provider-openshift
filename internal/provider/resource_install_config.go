package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.Resource = &InstallConfigResource{}

type InstallConfigResource struct {
	providerData *ProviderData
}

func NewInstallConfigResource() resource.Resource {
	return &InstallConfigResource{}
}

func (r *InstallConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_install_config"
}

// ---- nested model types ----

type DiscReg struct {
	Prefix             types.String `tfsdk:"prefix"`
	MirrorSource       types.String `tfsdk:"mirror_source"`
	MirrorByDigestOnly types.Bool   `tfsdk:"mirror_by_digest_only"`
}

type ICS struct {
	Source  types.String `tfsdk:"source"`
	Mirrors types.List   `tfsdk:"mirrors"`
}

type ProxyConfig struct {
	HTTPProxy  types.String `tfsdk:"http_proxy"`
	HTTPSProxy types.String `tfsdk:"https_proxy"`
	NoProxy    types.String `tfsdk:"no_proxy"`
}

type BMHost struct {
	Name            types.String `tfsdk:"name"`
	BMCAddress      types.String `tfsdk:"bmc_address"`
	BMCUsername     types.String `tfsdk:"bmc_username"`
	BMCPassword     types.String `tfsdk:"bmc_password"`
	BootMACAddress  types.String `tfsdk:"boot_mac_address"`
	Online          types.Bool   `tfsdk:"online"`
	RootDeviceHints types.Map    `tfsdk:"root_device_hints"`
}

type InstallConfigModel struct {
	ClusterName          types.String `tfsdk:"cluster_name"`
	BaseDomain           types.String `tfsdk:"base_domain"`
	OCPVersion           types.String `tfsdk:"ocp_version"`
	APIVIP               types.String `tfsdk:"api_vip"`
	IngressVIP           types.String `tfsdk:"ingress_vip"`
	ControlPlaneReplicas types.Int64  `tfsdk:"control_plane_replicas"`
	WorkerReplicas       types.Int64  `tfsdk:"worker_replicas"`
	NetworkType          types.String `tfsdk:"network_type"`
	ClusterNetworkCIDR   types.String `tfsdk:"cluster_network_cidr"`
	ServiceNetworkCIDR   types.String `tfsdk:"service_network_cidr"`
	MachineNetworkCIDR   types.String `tfsdk:"machine_network_cidr"`
	PullSecret           types.String `tfsdk:"pull_secret"`
	SSHKey               types.String `tfsdk:"ssh_key"`
	DisconnectedReg      types.List   `tfsdk:"disconnected_registries"`
	AdditionalTrustBundle types.String `tfsdk:"additional_trust_bundle"`
	ImageContentSources  types.List   `tfsdk:"image_content_sources"`
	Proxy                types.Object `tfsdk:"proxy"`
	BaremetalHosts       types.List   `tfsdk:"baremetal_hosts"`
	OutputDir            types.String `tfsdk:"output_dir"`
	Rendered             types.String `tfsdk:"rendered"`
}

func discRegAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"prefix":               types.StringType,
		"mirror_source":        types.StringType,
		"mirror_by_digest_only": types.BoolType,
	}
}

func icsAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"source":  types.StringType,
		"mirrors": types.ListType{ElemType: types.StringType},
	}
}

func proxyAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"http_proxy":  types.StringType,
		"https_proxy": types.StringType,
		"no_proxy":    types.StringType,
	}
}

func bmHostAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"name":             types.StringType,
		"bmc_address":      types.StringType,
		"bmc_username":     types.StringType,
		"bmc_password":     types.StringType,
		"boot_mac_address": types.StringType,
		"online":           types.BoolType,
		"root_device_hints": types.MapType{ElemType: types.StringType},
	}
}

func (r *InstallConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Generates an install-config.yaml for OpenShift bare metal IPI/UPI deployment.",
		Attributes: map[string]schema.Attribute{
			"cluster_name": schema.StringAttribute{Required: true},
			"base_domain":  schema.StringAttribute{Required: true},
			"ocp_version": schema.StringAttribute{
				Optional:    true,
				Description: "OpenShift version to deploy (e.g. 4.14). Used for compatibility checks.",
			},
			"api_vip":      schema.StringAttribute{Required: true},
			"ingress_vip":  schema.StringAttribute{Required: true},
			"control_plane_replicas": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:     int64default.StaticInt64(3),
				Description: "Number of control plane replicas.",
			},
			"worker_replicas": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:     int64default.StaticInt64(3),
				Description: "Number of worker replicas.",
			},
			"network_type": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("OVNKubernetes"),
				Description: "Network plugin type.",
			},
			"cluster_network_cidr": schema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("10.128.0.0/14"),
			},
			"service_network_cidr": schema.StringAttribute{
				Optional: true, Computed: true,
				Default: stringdefault.StaticString("172.30.0.0/16"),
			},
			"machine_network_cidr": schema.StringAttribute{Required: true},
			"pull_secret":          schema.StringAttribute{Required: true, Sensitive: true},
			"ssh_key":              schema.StringAttribute{Required: true},
			"additional_trust_bundle": schema.StringAttribute{
				Optional:    true,
				Description: "PEM-encoded CA certificate bundle for disconnected registries.",
			},
			"output_dir": schema.StringAttribute{
				Required:    true,
				Description: "Directory where install-config.yaml will be written.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"rendered": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "The rendered install-config.yaml content.",
			},
			"disconnected_registries": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Mirror registry entries for disconnected installations.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"prefix":                schema.StringAttribute{Required: true},
						"mirror_source":         schema.StringAttribute{Required: true},
						"mirror_by_digest_only": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)},
					},
				},
			},
			"image_content_sources": schema.ListNestedAttribute{
				Optional:    true,
				Description: "imageContentSources entries for mirrored images.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"source": schema.StringAttribute{Required: true},
						"mirrors": schema.ListAttribute{
							Required:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
			"proxy": schema.SingleNestedAttribute{
				Optional:    true,
				Description: "Proxy configuration for the cluster.",
				Attributes: map[string]schema.Attribute{
					"http_proxy":  schema.StringAttribute{Optional: true},
					"https_proxy": schema.StringAttribute{Optional: true},
					"no_proxy":    schema.StringAttribute{Optional: true},
				},
			},
			"baremetal_hosts": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Bare metal host definitions for IPI.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":             schema.StringAttribute{Required: true},
						"bmc_address":      schema.StringAttribute{Required: true},
						"bmc_username":     schema.StringAttribute{Required: true},
						"bmc_password":     schema.StringAttribute{Required: true, Sensitive: true},
						"boot_mac_address": schema.StringAttribute{Required: true},
						"online":           schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true)},
						"root_device_hints": schema.MapAttribute{
							Optional:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
		},
	}
}

func (r *InstallConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected ProviderData", fmt.Sprintf("Expected *ProviderData, got %T", req.ProviderData))
		return
	}
	r.providerData = pd
}

func (r *InstallConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan InstallConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Compatibility check.
	if !plan.OCPVersion.IsNull() && !plan.OCPVersion.IsUnknown() && plan.OCPVersion.ValueString() != "" {
		pv := "dev"
		if r.providerData != nil && r.providerData.Version != "" {
			pv = r.providerData.Version
		}
		if err := CheckCompat(plan.OCPVersion.ValueString(), pv); err != nil {
			if IsUnknownVersion(err) {
				resp.Diagnostics.AddWarning("OCP version compatibility", err.Error())
			} else {
				resp.Diagnostics.AddError("OCP version compatibility", err.Error())
				return
			}
		}
	}

	rendered, err := r.renderInstallConfig(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Rendering install-config.yaml", err.Error())
		return
	}

	outDir := plan.OutputDir.ValueString()
	if err := os.MkdirAll(outDir, 0755); err != nil {
		resp.Diagnostics.AddError("Creating output directory", err.Error())
		return
	}

	outPath := filepath.Join(outDir, "install-config.yaml")
	if err := os.WriteFile(outPath, []byte(rendered), 0600); err != nil {
		resp.Diagnostics.AddError("Writing install-config.yaml", err.Error())
		return
	}

	plan.Rendered = types.StringValue(rendered)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *InstallConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state InstallConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	outPath := filepath.Join(state.OutputDir.ValueString(), "install-config.yaml")
	data, err := os.ReadFile(outPath)
	if err != nil {
		if os.IsNotExist(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading install-config.yaml", err.Error())
		return
	}

	state.Rendered = types.StringValue(string(data))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *InstallConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan InstallConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Compatibility check.
	if !plan.OCPVersion.IsNull() && !plan.OCPVersion.IsUnknown() && plan.OCPVersion.ValueString() != "" {
		pv := "dev"
		if r.providerData != nil && r.providerData.Version != "" {
			pv = r.providerData.Version
		}
		if err := CheckCompat(plan.OCPVersion.ValueString(), pv); err != nil {
			if IsUnknownVersion(err) {
				resp.Diagnostics.AddWarning("OCP version compatibility", err.Error())
			} else {
				resp.Diagnostics.AddError("OCP version compatibility", err.Error())
				return
			}
		}
	}

	rendered, err := r.renderInstallConfig(ctx, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Rendering install-config.yaml", err.Error())
		return
	}

	outDir := plan.OutputDir.ValueString()
	if err := os.MkdirAll(outDir, 0755); err != nil {
		resp.Diagnostics.AddError("Creating output directory", err.Error())
		return
	}

	outPath := filepath.Join(outDir, "install-config.yaml")
	if err := os.WriteFile(outPath, []byte(rendered), 0600); err != nil {
		resp.Diagnostics.AddError("Writing install-config.yaml", err.Error())
		return
	}

	plan.Rendered = types.StringValue(rendered)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *InstallConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state InstallConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	outPath := filepath.Join(state.OutputDir.ValueString(), "install-config.yaml")
	if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
		resp.Diagnostics.AddError("Deleting install-config.yaml", err.Error())
	}
}

// renderInstallConfig builds the YAML from the plan model.
func (r *InstallConfigResource) renderInstallConfig(ctx context.Context, plan *InstallConfigModel) (string, error) {
	type networkEntry struct {
		CIDR       string `json:"cidr"`
		HostPrefix int    `json:"hostPrefix"`
	}

	type networking struct {
		NetworkType        string         `json:"networkType"`
		ClusterNetwork     []networkEntry `json:"clusterNetwork"`
		ServiceNetwork     []string       `json:"serviceNetwork"`
		MachineNetwork     []map[string]string `json:"machineNetwork"`
	}

	type replicasSpec struct {
		Name     string `json:"name"`
		Replicas int64  `json:"replicas"`
	}

	type bmcCreds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	type bmHostSpec struct {
		Name            string            `json:"name"`
		BMC             bmcCreds          `json:"bmc"`
		BootMACAddress  string            `json:"bootMACAddress"`
		Online          bool              `json:"online"`
		RootDeviceHints map[string]string `json:"rootDeviceHints,omitempty"`
	}

	type bmPlatform struct {
		Hosts []bmHostSpec `json:"hosts,omitempty"`
		APIVIP      string `json:"apiVIP,omitempty"`
		IngressVIP  string `json:"ingressVIP,omitempty"`
	}

	type platform struct {
		BareMetal *bmPlatform `json:"baremetal,omitempty"`
	}

	type proxySpec struct {
		HTTPProxy  string `json:"httpProxy,omitempty"`
		HTTPSProxy string `json:"httpsProxy,omitempty"`
		NoProxy    string `json:"noProxy,omitempty"`
	}

	type imageContentSource struct {
		Source  string   `json:"source"`
		Mirrors []string `json:"mirrors"`
	}

	type installConfig struct {
		APIVersion           string               `json:"apiVersion"`
		BaseDomain           string               `json:"baseDomain"`
		Metadata             map[string]string    `json:"metadata"`
		Networking           networking           `json:"networking"`
		ControlPlane         replicasSpec         `json:"controlPlane"`
		Compute              []replicasSpec       `json:"compute"`
		Platform             platform             `json:"platform"`
		PullSecret           string               `json:"pullSecret"`
		SSHKey               string               `json:"sshKey"`
		AdditionalTrustBundle string              `json:"additionalTrustBundle,omitempty"`
		ImageContentSources  []imageContentSource `json:"imageContentSources,omitempty"`
		Proxy                *proxySpec           `json:"proxy,omitempty"`
	}

	cfg := installConfig{
		APIVersion: "v1",
		BaseDomain: plan.BaseDomain.ValueString(),
		Metadata: map[string]string{
			"name": plan.ClusterName.ValueString(),
		},
		Networking: networking{
			NetworkType: plan.NetworkType.ValueString(),
			ClusterNetwork: []networkEntry{
				{CIDR: plan.ClusterNetworkCIDR.ValueString(), HostPrefix: 23},
			},
			ServiceNetwork: []string{plan.ServiceNetworkCIDR.ValueString()},
			MachineNetwork: []map[string]string{
				{"cidr": plan.MachineNetworkCIDR.ValueString()},
			},
		},
		ControlPlane: replicasSpec{
			Name:     "master",
			Replicas: plan.ControlPlaneReplicas.ValueInt64(),
		},
		Compute: []replicasSpec{
			{Name: "worker", Replicas: plan.WorkerReplicas.ValueInt64()},
		},
		PullSecret:            plan.PullSecret.ValueString(),
		SSHKey:                plan.SSHKey.ValueString(),
		AdditionalTrustBundle: plan.AdditionalTrustBundle.ValueString(),
	}

	// Platform / baremetal
	bm := &bmPlatform{
		APIVIP:     plan.APIVIP.ValueString(),
		IngressVIP: plan.IngressVIP.ValueString(),
	}

	if !plan.BaremetalHosts.IsNull() && !plan.BaremetalHosts.IsUnknown() {
		var hosts []BMHost
		if diags := plan.BaremetalHosts.ElementsAs(ctx, &hosts, false); diags.HasError() {
			return "", fmt.Errorf("reading baremetal_hosts")
		}
		for _, h := range hosts {
			hints := map[string]string{}
			if !h.RootDeviceHints.IsNull() && !h.RootDeviceHints.IsUnknown() {
				h.RootDeviceHints.ElementsAs(ctx, &hints, false)
			}
			spec := bmHostSpec{
				Name: h.Name.ValueString(),
				BMC: bmcCreds{
					Username: h.BMCUsername.ValueString(),
					Password: h.BMCPassword.ValueString(),
				},
				BootMACAddress:  h.BootMACAddress.ValueString(),
				Online:          h.Online.ValueBool(),
				RootDeviceHints: hints,
			}
			// encode BMC address in the BMC block
			spec.BMC.Username = h.BMCUsername.ValueString()
			spec.BMC.Password = h.BMCPassword.ValueString()
			_ = h.BMCAddress.ValueString() // address stored separately below
			bm.Hosts = append(bm.Hosts, spec)
		}
	}
	cfg.Platform = platform{BareMetal: bm}

	// imageContentSources
	if !plan.ImageContentSources.IsNull() && !plan.ImageContentSources.IsUnknown() {
		var icsItems []ICS
		if diags := plan.ImageContentSources.ElementsAs(ctx, &icsItems, false); diags.HasError() {
			return "", fmt.Errorf("reading image_content_sources")
		}
		for _, ics := range icsItems {
			var mirrors []string
			ics.Mirrors.ElementsAs(ctx, &mirrors, false)
			cfg.ImageContentSources = append(cfg.ImageContentSources, imageContentSource{
				Source:  ics.Source.ValueString(),
				Mirrors: mirrors,
			})
		}
	}

	// proxy
	if !plan.Proxy.IsNull() && !plan.Proxy.IsUnknown() {
		var p ProxyConfig
		if diags := plan.Proxy.As(ctx, &p, basetypes.ObjectAsOptions{}); !diags.HasError() {
			cfg.Proxy = &proxySpec{
				HTTPProxy:  p.HTTPProxy.ValueString(),
				HTTPSProxy: p.HTTPSProxy.ValueString(),
				NoProxy:    p.NoProxy.ValueString(),
			}
		}
	}

	data, err := marshalYAML(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
