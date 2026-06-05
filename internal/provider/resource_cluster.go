package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	tflog "github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &ClusterResource{}

// ClusterResource manages an OpenShift cluster via the Assisted Installer API.
type ClusterResource struct {
	providerData *ProviderData
}

func NewClusterResource() resource.Resource {
	return &ClusterResource{}
}

func (r *ClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster"
}

// ---- model ------------------------------------------------------------------

type imageContentSourceModel struct {
	Source  types.String `tfsdk:"source"`
	Mirrors types.List   `tfsdk:"mirrors"`
}

type hostModel struct {
	ID       types.String `tfsdk:"id"`
	Hostname types.String `tfsdk:"hostname"`
	Role     types.String `tfsdk:"role"`
	Status   types.String `tfsdk:"status"`
}

type ClusterModel struct {
	// Required
	AssistedServiceURL  types.String `tfsdk:"assisted_service_url"`
	ClusterName         types.String `tfsdk:"cluster_name"`
	OpenshiftVersion    types.String `tfsdk:"openshift_version"`
	BaseDNSDomain       types.String `tfsdk:"base_dns_domain"`
	APIVIP              types.String `tfsdk:"api_vip"`
	IngressVIP          types.String `tfsdk:"ingress_vip"`
	MachineNetworkCIDR  types.String `tfsdk:"machine_network_cidr"`
	SSHPublicKey        types.String `tfsdk:"ssh_public_key"`
	PullSecret          types.String `tfsdk:"pull_secret"`

	// Optional
	AssistedServiceToken  types.String `tfsdk:"assisted_service_token"`
	NetworkType           types.String `tfsdk:"network_type"`
	ClusterNetworkCIDR    types.String `tfsdk:"cluster_network_cidr"`
	ServiceNetworkCIDR    types.String `tfsdk:"service_network_cidr"`
	ControlPlaneReplicas  types.Int64  `tfsdk:"control_plane_replicas"`
	WorkerReplicas        types.Int64  `tfsdk:"worker_replicas"`
	AdditionalTrustBundle types.String `tfsdk:"additional_trust_bundle"`
	HTTPProxy             types.String `tfsdk:"http_proxy"`
	HTTPSProxy            types.String `tfsdk:"https_proxy"`
	NoProxy               types.String `tfsdk:"no_proxy"`
	ImageContentSources   types.List   `tfsdk:"image_content_sources"`
	CreateInfraEnv        types.Bool   `tfsdk:"create_infra_env"`
	InfraEnvImageType     types.String `tfsdk:"infra_env_image_type"`
	WaitForHosts          types.Int64  `tfsdk:"wait_for_hosts"`
	HostWaitTimeout       types.String `tfsdk:"host_wait_timeout"`
	AutoInstall           types.Bool   `tfsdk:"auto_install"`
	InstallTimeout        types.String `tfsdk:"install_timeout"`
	DestroyOnDelete       types.Bool   `tfsdk:"destroy_on_delete"`

	// Computed
	ClusterID           types.String `tfsdk:"cluster_id"`
	InfraEnvID          types.String `tfsdk:"infra_env_id"`
	DiscoveryISOURL     types.String `tfsdk:"discovery_iso_url"`
	Status              types.String `tfsdk:"status"`
	StatusInfo          types.String `tfsdk:"status_info"`
	ConsoleURL          types.String `tfsdk:"console_url"`
	APIURL              types.String `tfsdk:"api_url"`
	KubeadminPassword   types.String `tfsdk:"kubeadmin_password"`
	Hosts               types.List   `tfsdk:"hosts"`
}

// ---- schema -----------------------------------------------------------------

var hostAttrTypes = map[string]attr.Type{
	"id":       types.StringType,
	"hostname": types.StringType,
	"role":     types.StringType,
	"status":   types.StringType,
}

var clusterICSAttrTypes = map[string]attr.Type{
	"source":  types.StringType,
	"mirrors": types.ListType{ElemType: types.StringType},
}

func (r *ClusterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an OpenShift cluster via the Assisted Installer API.",
		Attributes: map[string]schema.Attribute{
			// Required
			"assisted_service_url": schema.StringAttribute{
				Optional:    true,
				Description: "Base URL of the Assisted Installer service. Defaults to the provider-level assisted_service_url.",
			},
			"cluster_name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the cluster.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"openshift_version": schema.StringAttribute{
				Required:    true,
				Description: "OpenShift version, e.g. '4.14'.",
			},
			"base_dns_domain": schema.StringAttribute{
				Required:    true,
				Description: "Base DNS domain for the cluster.",
			},
			"api_vip": schema.StringAttribute{
				Required:    true,
				Description: "Virtual IP address for the cluster API.",
			},
			"ingress_vip": schema.StringAttribute{
				Required:    true,
				Description: "Virtual IP address for the cluster ingress.",
			},
			"machine_network_cidr": schema.StringAttribute{
				Required:    true,
				Description: "CIDR block for the machine network.",
			},
			"ssh_public_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "SSH public key for cluster nodes.",
			},
			"pull_secret": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Red Hat pull secret JSON.",
			},

			// Optional
			"assisted_service_token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Bearer token for the Assisted Installer API. Overrides provider-level token.",
			},
			"network_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("OVNKubernetes"),
				Description: "Network plugin type.",
			},
			"cluster_network_cidr": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("10.128.0.0/14"),
				Description: "CIDR for the cluster pod network.",
			},
			"service_network_cidr": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("172.30.0.0/16"),
				Description: "CIDR for the service network.",
			},
			"control_plane_replicas": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(3),
				Description: "Number of control plane nodes.",
			},
			"worker_replicas": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(3),
				Description: "Number of worker nodes.",
			},
			"additional_trust_bundle": schema.StringAttribute{
				Optional:    true,
				Description: "PEM-encoded additional CA certificate bundle.",
			},
			"http_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "HTTP proxy URL.",
			},
			"https_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "HTTPS proxy URL.",
			},
			"no_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "Comma-separated list of hosts/CIDRs to bypass the proxy.",
			},
			"image_content_sources": schema.ListNestedAttribute{
				Optional:    true,
				Description: "Disconnected registry mirror configuration.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"source": schema.StringAttribute{
							Required:    true,
							Description: "Source registry.",
						},
						"mirrors": schema.ListAttribute{
							Required:    true,
							ElementType: types.StringType,
							Description: "Mirror registries for the source.",
						},
					},
				},
			},
			"create_infra_env": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Whether to create an infra-env and generate a discovery ISO.",
			},
			"infra_env_image_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("minimal-iso"),
				Description: "Discovery ISO type: 'full-iso' or 'minimal-iso'.",
			},
			"wait_for_hosts": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(0),
				Description: "Block until this many hosts register in the infra-env (0 = do not wait).",
			},
			"host_wait_timeout": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("60m"),
				Description: "Duration to wait for hosts to register.",
			},
			"auto_install": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Trigger installation automatically once the cluster reaches 'ready' status.",
			},
			"install_timeout": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("120m"),
				Description: "Duration to wait for installation to complete.",
			},
			"destroy_on_delete": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Delete the cluster via the API when this resource is destroyed.",
			},

			// Computed
			"cluster_id": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster UUID assigned by the Assisted Installer.",
			},
			"infra_env_id": schema.StringAttribute{
				Computed:    true,
				Description: "Infra-env UUID assigned by the Assisted Installer.",
			},
			"discovery_iso_url": schema.StringAttribute{
				Computed:    true,
				Description: "Download URL for the discovery ISO.",
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Current cluster status.",
			},
			"status_info": schema.StringAttribute{
				Computed:    true,
				Description: "Human-readable status detail.",
			},
			"console_url": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster web console URL.",
			},
			"api_url": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster API URL.",
			},
			"kubeadmin_password": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "kubeadmin password (available after installation).",
			},
			"hosts": schema.ListNestedAttribute{
				Computed:    true,
				Description: "Hosts registered in the infra-env.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:    true,
							Description: "Host UUID.",
						},
						"hostname": schema.StringAttribute{
							Computed:    true,
							Description: "Requested hostname.",
						},
						"role": schema.StringAttribute{
							Computed:    true,
							Description: "Host role: master or worker.",
						},
						"status": schema.StringAttribute{
							Computed:    true,
							Description: "Host status.",
						},
					},
				},
			},
		},
	}
}

// ---- provider wiring --------------------------------------------------------

func (r *ClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ---- helpers ----------------------------------------------------------------

func (r *ClusterResource) buildClient(plan *ClusterModel) *AssistedClient {
	baseURL := plan.AssistedServiceURL.ValueString()
	if baseURL == "" && r.providerData != nil {
		baseURL = r.providerData.AssistedServiceURL
	}
	pullSecret := plan.PullSecret.ValueString()

	// Resource-level static token takes highest precedence
	if !plan.AssistedServiceToken.IsNull() && !plan.AssistedServiceToken.IsUnknown() {
		return NewAssistedClient(baseURL, plan.AssistedServiceToken.ValueString(), pullSecret)
	}

	// Provider-level static token
	if r.providerData != nil && r.providerData.AssistedServiceToken != "" {
		return NewAssistedClient(baseURL, r.providerData.AssistedServiceToken, pullSecret)
	}

	// Provider-level offline token (auto-refreshing)
	if r.providerData != nil && r.providerData.AssistedTokenManager != nil {
		return NewAssistedClientWithManager(baseURL, r.providerData.AssistedTokenManager, pullSecret)
	}

	return NewAssistedClient(baseURL, "", pullSecret)
}

func strPtr(s string) *string { return &s }

func nullableString(t types.String) string {
	if t.IsNull() || t.IsUnknown() {
		return ""
	}
	return t.ValueString()
}

func (r *ClusterResource) buildCreateParams(plan *ClusterModel, ctx context.Context) (CreateClusterParams, error) {
	params := CreateClusterParams{
		Name:                  plan.ClusterName.ValueString(),
		OpenshiftVersion:      plan.OpenshiftVersion.ValueString(),
		BaseDNSDomain:         plan.BaseDNSDomain.ValueString(),
		APIVIP:                plan.APIVIP.ValueString(),
		IngressVIP:            plan.IngressVIP.ValueString(),
		NetworkType:           nullableString(plan.NetworkType),
		ClusterNetworkCIDR:    nullableString(plan.ClusterNetworkCIDR),
		ServiceNetworkCIDR:    nullableString(plan.ServiceNetworkCIDR),
		MachineNetworkCIDR:    plan.MachineNetworkCIDR.ValueString(),
		SSHPublicKey:          plan.SSHPublicKey.ValueString(),
		PullSecret:            plan.PullSecret.ValueString(),
		CPUReplicas:           plan.ControlPlaneReplicas.ValueInt64(),
		WorkerReplicas:        plan.WorkerReplicas.ValueInt64(),
		AdditionalTrustBundle: nullableString(plan.AdditionalTrustBundle),
		HTTPProxy:             nullableString(plan.HTTPProxy),
		HTTPSProxy:            nullableString(plan.HTTPSProxy),
		NoProxy:               nullableString(plan.NoProxy),
	}

	if !plan.ImageContentSources.IsNull() && !plan.ImageContentSources.IsUnknown() {
		var icsList []imageContentSourceModel
		if diags := plan.ImageContentSources.ElementsAs(ctx, &icsList, false); diags.HasError() {
			return params, fmt.Errorf("parsing image_content_sources")
		}
		for _, ics := range icsList {
			var mirrors []string
			if diags := ics.Mirrors.ElementsAs(ctx, &mirrors, false); diags.HasError() {
				return params, fmt.Errorf("parsing mirrors")
			}
			params.ImageContentSources = append(params.ImageContentSources, ImageContentSource{
				Source:  ics.Source.ValueString(),
				Mirrors: mirrors,
			})
		}
	}

	return params, nil
}

func hostsToListValue(hosts []Host) (types.List, error) {
	elems := make([]attr.Value, len(hosts))
	for i, h := range hosts {
		hostname := h.RequestedHostname
		if hostname == "" {
			hostname = h.Hostname
		}
		obj, diags := types.ObjectValue(hostAttrTypes, map[string]attr.Value{
			"id":       types.StringValue(h.ID),
			"hostname": types.StringValue(hostname),
			"role":     types.StringValue(h.Role),
			"status":   types.StringValue(h.Status),
		})
		if diags.HasError() {
			return types.ListNull(types.ObjectType{AttrTypes: hostAttrTypes}), fmt.Errorf("building host object")
		}
		elems[i] = obj
	}
	listVal, diags := types.ListValue(types.ObjectType{AttrTypes: hostAttrTypes}, elems)
	if diags.HasError() {
		return types.ListNull(types.ObjectType{AttrTypes: hostAttrTypes}), fmt.Errorf("building hosts list")
	}
	return listVal, nil
}

func emptyHostsList() types.List {
	return types.ListValueMust(types.ObjectType{AttrTypes: hostAttrTypes}, []attr.Value{})
}

func emptyICSList() types.List {
	return types.ListValueMust(types.ObjectType{AttrTypes: clusterICSAttrTypes}, []attr.Value{})
}

func applyClusterToModel(cl *Cluster, model *ClusterModel) {
	model.ClusterID = types.StringValue(cl.ID)
	model.Status = types.StringValue(cl.Status)
	model.StatusInfo = types.StringValue(cl.StatusInfo)
	model.ConsoleURL = types.StringValue(cl.ConsoleURL)
	model.APIURL = types.StringValue(cl.APIURL)
}

func applyInfraEnvToModel(ie *InfraEnv, model *ClusterModel) {
	model.InfraEnvID = types.StringValue(ie.ID)
	model.DiscoveryISOURL = types.StringValue(ie.DownloadURL)
}

// waitForInstall polls GetCluster every 30 s until status is "installed" or "error".
func (r *ClusterResource) waitForInstall(ctx context.Context, client *AssistedClient, clusterID string, timeout time.Duration) (*Cluster, error) {
	deadline := time.Now().Add(timeout)
	for {
		cl, err := client.GetCluster(ctx, clusterID)
		if err != nil {
			return nil, fmt.Errorf("polling cluster status: %w", err)
		}
		tflog.Info(ctx, "cluster install status", map[string]interface{}{
			"status":      cl.Status,
			"status_info": cl.StatusInfo,
		})
		switch cl.Status {
		case "installed":
			return cl, nil
		case "error", "cancelled":
			return cl, fmt.Errorf("cluster installation %s: %s", cl.Status, cl.StatusInfo)
		}
		if time.Now().After(deadline) {
			return cl, fmt.Errorf("timed out after %s waiting for cluster installation; last status: %s (%s)", timeout, cl.Status, cl.StatusInfo)
		}
		select {
		case <-ctx.Done():
			return cl, ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
}

// ---- CRUD -------------------------------------------------------------------

func (r *ClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Compatibility check
	ocpVersion := plan.OpenshiftVersion.ValueString()
	if compatErr := CheckCompat(ocpVersion, r.providerVersion()); compatErr != nil {
		if IsUnknownVersion(compatErr) {
			resp.Diagnostics.AddWarning("OCP version compatibility", compatErr.Error())
		} else {
			resp.Diagnostics.AddError("OCP version compatibility", compatErr.Error())
			return
		}
	}

	client := r.buildClient(&plan)

	// Initialize computed fields so state is always valid.
	plan.ClusterID = types.StringValue("")
	plan.InfraEnvID = types.StringValue("")
	plan.DiscoveryISOURL = types.StringValue("")
	plan.Status = types.StringValue("")
	plan.StatusInfo = types.StringValue("")
	plan.ConsoleURL = types.StringValue("")
	plan.APIURL = types.StringValue("")
	plan.KubeadminPassword = types.StringValue("")
	plan.Hosts = emptyHostsList()
	if plan.ImageContentSources.IsNull() || plan.ImageContentSources.IsUnknown() {
		plan.ImageContentSources = emptyICSList()
	}

	// 1. Create cluster
	createParams, err := r.buildCreateParams(&plan, ctx)
	if err != nil {
		resp.Diagnostics.AddError("Building cluster params", err.Error())
		return
	}
	cl, err := client.CreateCluster(ctx, createParams)
	if err != nil {
		resp.Diagnostics.AddError("Creating cluster", err.Error())
		return
	}
	applyClusterToModel(cl, &plan)

	// 2. Create infra-env
	if plan.CreateInfraEnv.ValueBool() {
		var proxy *AssistedProxyConfig
		if nullableString(plan.HTTPProxy) != "" || nullableString(plan.HTTPSProxy) != "" {
			proxy = &AssistedProxyConfig{
				HTTPProxy:  nullableString(plan.HTTPProxy),
				HTTPSProxy: nullableString(plan.HTTPSProxy),
				NoProxy:    nullableString(plan.NoProxy),
			}
		}
		ie, err := client.CreateInfraEnv(ctx, CreateInfraEnvParams{
			Name:                  plan.ClusterName.ValueString() + "-infra-env",
			ClusterID:             cl.ID,
			SSHPublicKey:          plan.SSHPublicKey.ValueString(),
			PullSecret:            plan.PullSecret.ValueString(),
			ImageType:             nullableString(plan.InfraEnvImageType),
			OpenshiftVersion:      plan.OpenshiftVersion.ValueString(),
			Proxy:                 proxy,
			AdditionalTrustBundle: nullableString(plan.AdditionalTrustBundle),
		})
		if err != nil {
			resp.Diagnostics.AddError("Creating infra-env", err.Error())
			// Save partial state with cluster ID so we can clean up on next apply.
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			return
		}
		applyInfraEnvToModel(ie, &plan)

		// 3. Wait for hosts
		waitCount := int(plan.WaitForHosts.ValueInt64())
		if waitCount > 0 {
			waitTimeoutStr := nullableString(plan.HostWaitTimeout)
			if waitTimeoutStr == "" {
				waitTimeoutStr = "60m"
			}
			waitTimeout, parseErr := time.ParseDuration(waitTimeoutStr)
			if parseErr != nil {
				resp.Diagnostics.AddError("Invalid host_wait_timeout", parseErr.Error())
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				return
			}
			hosts, waitErr := client.WaitForHostsRegistered(ctx, ie.ID, waitCount, waitTimeout)
			if waitErr != nil {
				resp.Diagnostics.AddError("Waiting for hosts", waitErr.Error())
			}
			// Always update hosts list even on partial success.
			if hostList, listErr := hostsToListValue(hosts); listErr == nil {
				plan.Hosts = hostList
			}
			if resp.Diagnostics.HasError() {
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				return
			}
		}
	}

	// 4. Auto-install
	if plan.AutoInstall.ValueBool() {
		// Refresh cluster status before deciding to install.
		cl, err = client.GetCluster(ctx, cl.ID)
		if err != nil {
			resp.Diagnostics.AddError("Refreshing cluster before install", err.Error())
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			return
		}
		applyClusterToModel(cl, &plan)

		if cl.Status == "ready" {
			if err := client.InstallCluster(ctx, cl.ID); err != nil {
				resp.Diagnostics.AddError("Triggering cluster installation", err.Error())
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				return
			}

			installTimeoutStr := nullableString(plan.InstallTimeout)
			if installTimeoutStr == "" {
				installTimeoutStr = "120m"
			}
			installTimeout, parseErr := time.ParseDuration(installTimeoutStr)
			if parseErr != nil {
				resp.Diagnostics.AddError("Invalid install_timeout", parseErr.Error())
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				return
			}

			finalCl, waitErr := r.waitForInstall(ctx, client, cl.ID, installTimeout)
			if finalCl != nil {
				applyClusterToModel(finalCl, &plan)
			}
			if waitErr != nil {
				resp.Diagnostics.AddError("Waiting for installation", waitErr.Error())
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				return
			}

			// Fetch kubeadmin credentials.
			creds, credErr := client.GetCredentials(ctx, cl.ID)
			if credErr == nil && creds != nil {
				plan.KubeadminPassword = types.StringValue(creds.Password)
				if creds.ConsoleURL != "" && plan.ConsoleURL.ValueString() == "" {
					plan.ConsoleURL = types.StringValue(creds.ConsoleURL)
				}
			}
		} else {
			resp.Diagnostics.AddWarning(
				"Cluster not ready for installation",
				fmt.Sprintf("auto_install=true but cluster status is %q (need 'ready'); skipping install trigger.", cl.Status),
			)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		// Nothing to read — resource was not fully created.
		return
	}

	client := r.buildClient(&state)

	cl, err := client.GetCluster(ctx, clusterID)
	if err != nil {
		if apiErr, ok := err.(*APIError); ok && apiErr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading cluster", err.Error())
		return
	}
	applyClusterToModel(cl, &state)

	// Read infra-env if we have an ID.
	if infraEnvID := state.InfraEnvID.ValueString(); infraEnvID != "" {
		ie, ieErr := client.GetInfraEnv(ctx, infraEnvID)
		if ieErr == nil {
			applyInfraEnvToModel(ie, &state)
		}
		// List hosts.
		hosts, hostsErr := client.ListHosts(ctx, infraEnvID)
		if hostsErr == nil {
			if hostList, listErr := hostsToListValue(hosts); listErr == nil {
				state.Hosts = hostList
			}
		}
	}

	// Refresh kubeadmin creds if installed.
	if cl.Status == "installed" && state.KubeadminPassword.ValueString() == "" {
		creds, credErr := client.GetCredentials(ctx, clusterID)
		if credErr == nil && creds != nil {
			state.KubeadminPassword = types.StringValue(creds.Password)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	clusterID := state.ClusterID.ValueString()
	if clusterID == "" {
		resp.Diagnostics.AddError("Missing cluster ID", "Cannot update a cluster that was not fully created.")
		return
	}

	client := r.buildClient(&plan)

	// Build update params — only mutable fields.
	updateParams := UpdateClusterParams{}
	if plan.APIVIP.ValueString() != state.APIVIP.ValueString() {
		updateParams.APIVIP = strPtr(plan.APIVIP.ValueString())
	}
	if plan.IngressVIP.ValueString() != state.IngressVIP.ValueString() {
		updateParams.IngressVIP = strPtr(plan.IngressVIP.ValueString())
	}
	if plan.SSHPublicKey.ValueString() != state.SSHPublicKey.ValueString() {
		updateParams.SSHPublicKey = strPtr(plan.SSHPublicKey.ValueString())
	}

	if updateParams.APIVIP != nil || updateParams.IngressVIP != nil || updateParams.SSHPublicKey != nil {
		cl, err := client.UpdateCluster(ctx, clusterID, updateParams)
		if err != nil {
			resp.Diagnostics.AddError("Updating cluster", err.Error())
			return
		}
		applyClusterToModel(cl, &plan)
	} else {
		// No API changes needed — carry over computed fields.
		plan.ClusterID = state.ClusterID
		plan.Status = state.Status
		plan.StatusInfo = state.StatusInfo
		plan.ConsoleURL = state.ConsoleURL
		plan.APIURL = state.APIURL
	}

	// Carry over other computed fields.
	plan.InfraEnvID = state.InfraEnvID
	plan.DiscoveryISOURL = state.DiscoveryISOURL
	plan.KubeadminPassword = state.KubeadminPassword
	plan.Hosts = state.Hosts

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.DestroyOnDelete.ValueBool() {
		return
	}

	client := r.buildClient(&state)

	// Delete infra-env first.
	if infraEnvID := state.InfraEnvID.ValueString(); infraEnvID != "" {
		if err := client.DeleteInfraEnv(ctx, infraEnvID); err != nil {
			if apiErr, ok := err.(*APIError); !ok || apiErr.StatusCode != 404 {
				resp.Diagnostics.AddWarning("Deleting infra-env", err.Error())
			}
		}
	}

	// Delete cluster.
	if clusterID := state.ClusterID.ValueString(); clusterID != "" {
		if err := client.DeleteCluster(ctx, clusterID); err != nil {
			if apiErr, ok := err.(*APIError); !ok || apiErr.StatusCode != 404 {
				resp.Diagnostics.AddError("Deleting cluster", err.Error())
			}
		}
	}
}

// providerVersion returns the provider version from ProviderData.
func (r *ClusterResource) providerVersion() string {
	if r.providerData != nil && r.providerData.Version != "" {
		return r.providerData.Version
	}
	return "dev"
}
