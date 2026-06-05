package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &ClusterResource{}

type ClusterResource struct {
	providerData *ProviderData
}

func NewClusterResource() resource.Resource {
	return &ClusterResource{}
}

func (r *ClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster"
}

type ClusterModel struct {
	InstallDir             types.String `tfsdk:"install_dir"`
	InstallBinary          types.String `tfsdk:"install_binary"`
	LogLevel               types.String `tfsdk:"log_level"`
	Timeout                types.String `tfsdk:"timeout"`
	DestroyOnDelete        types.Bool   `tfsdk:"destroy_on_delete"`
	APIURL                 types.String `tfsdk:"api_url"`
	ConsoleURL             types.String `tfsdk:"console_url"`
	KubeconfigPath         types.String `tfsdk:"kubeconfig_path"`
	KubeadminPasswordPath  types.String `tfsdk:"kubeadmin_password_path"`
	ClusterID              types.String `tfsdk:"cluster_id"`
}

func (r *ClusterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an OpenShift cluster lifecycle via openshift-install.",
		Attributes: map[string]schema.Attribute{
			"install_dir": schema.StringAttribute{
				Required:    true,
				Description: "Directory containing install-config.yaml.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"install_binary": schema.StringAttribute{
				Optional:    true,
				Description: "Path to openshift-install binary. Overrides provider setting.",
			},
			"log_level": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("info"),
				Description: "Log level for openshift-install (debug, info, warn, error).",
			},
			"timeout": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("90m"),
				Description: "Timeout for cluster creation/destruction (e.g. 90m, 2h).",
			},
			"destroy_on_delete": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Run openshift-install destroy cluster when this resource is deleted.",
			},
			"api_url": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster API URL.",
			},
			"console_url": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster web console URL.",
			},
			"kubeconfig_path": schema.StringAttribute{
				Computed:    true,
				Description: "Path to the generated kubeconfig file.",
			},
			"kubeadmin_password_path": schema.StringAttribute{
				Computed:    true,
				Description: "Path to the kubeadmin-password file.",
			},
			"cluster_id": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster infrastructure ID read from metadata.json.",
			},
		},
	}
}

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

func (r *ClusterResource) resolveInstallBinary(plan *ClusterModel) string {
	if !plan.InstallBinary.IsNull() && !plan.InstallBinary.IsUnknown() && plan.InstallBinary.ValueString() != "" {
		return plan.InstallBinary.ValueString()
	}
	if r.providerData != nil && r.providerData.InstallBinary != "" {
		return r.providerData.InstallBinary
	}
	return "openshift-install"
}

func (r *ClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	binary := r.resolveInstallBinary(&plan)
	installDir := plan.InstallDir.ValueString()
	logLevel := plan.LogLevel.ValueString()

	timeoutStr := plan.Timeout.ValueString()
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		resp.Diagnostics.AddError("Invalid timeout", fmt.Sprintf("Cannot parse timeout %q: %v", timeoutStr, err))
		return
	}

	args := []string{"create", "cluster", "--dir", installDir, "--log-level", logLevel}
	stdout, stderr, err := runCommand(ctx, binary, args, nil, timeout)
	if err != nil {
		resp.Diagnostics.AddError("openshift-install create cluster failed",
			fmt.Sprintf("stdout: %s\nstderr: %s\nerror: %v", stdout, stderr, err))
		return
	}

	if err := r.readClusterOutputs(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Reading cluster outputs", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	installDir := state.InstallDir.ValueString()
	metadataPath := filepath.Join(installDir, "metadata.json")
	if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}

	if err := r.readClusterOutputs(ctx, &state); err != nil {
		resp.Diagnostics.AddError("Reading cluster outputs", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// install_dir has RequiresReplace; only mutable fields are log_level, timeout, destroy_on_delete
	var plan ClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Re-read outputs to keep computed fields current
	if err := r.readClusterOutputs(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Reading cluster outputs", err.Error())
		return
	}

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

	binary := r.resolveInstallBinary(&state)
	installDir := state.InstallDir.ValueString()
	logLevel := state.LogLevel.ValueString()

	timeoutStr := state.Timeout.ValueString()
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		timeout = 90 * time.Minute
	}

	args := []string{"destroy", "cluster", "--dir", installDir, "--log-level", logLevel}
	stdout, stderr, err := runCommand(ctx, binary, args, nil, timeout)
	if err != nil {
		resp.Diagnostics.AddError("openshift-install destroy cluster failed",
			fmt.Sprintf("stdout: %s\nstderr: %s\nerror: %v", stdout, stderr, err))
	}
}

type clusterMetadata struct {
	InfraID     string `json:"infraID"`
	ClusterID   string `json:"clusterID"`
	ClusterName string `json:"clusterName"`
}

func (r *ClusterResource) readClusterOutputs(_ context.Context, model *ClusterModel) error {
	installDir := model.InstallDir.ValueString()

	// kubeconfig
	kubeconfigPath := filepath.Join(installDir, "auth", "kubeconfig")
	model.KubeconfigPath = types.StringValue(kubeconfigPath)

	// kubeadmin-password
	kubeadminPath := filepath.Join(installDir, "auth", "kubeadmin-password")
	model.KubeadminPasswordPath = types.StringValue(kubeadminPath)

	// metadata.json
	metadataPath := filepath.Join(installDir, "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			model.ClusterID = types.StringValue("")
			model.APIURL = types.StringValue("")
			model.ConsoleURL = types.StringValue("")
			return nil
		}
		return fmt.Errorf("reading metadata.json: %w", err)
	}

	var meta clusterMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parsing metadata.json: %w", err)
	}
	model.ClusterID = types.StringValue(meta.InfraID)

	// Derive URLs from cluster name and base domain stored in metadata if present,
	// or fall back to standard patterns using infraID.
	// The openshift-install creates a .openshift_install_state.json we can also use.
	// For simplicity we derive from metadata.
	if meta.ClusterName != "" {
		// We don't have base_domain in metadata; attempt to read from install-config backup.
		baseDomain := r.readBaseDomain(installDir)
		if baseDomain != "" {
			model.APIURL = types.StringValue(fmt.Sprintf("https://api.%s.%s:6443", meta.ClusterName, baseDomain))
			model.ConsoleURL = types.StringValue(fmt.Sprintf("https://console-openshift-console.apps.%s.%s", meta.ClusterName, baseDomain))
		} else {
			model.APIURL = types.StringValue("")
			model.ConsoleURL = types.StringValue("")
		}
	}

	return nil
}

func (r *ClusterResource) readBaseDomain(installDir string) string {
	// openshift-install copies install-config.yaml to install-config.yaml.bak before consuming it
	backupPath := filepath.Join(installDir, "install-config.yaml.bak")
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return ""
	}
	var ic map[string]interface{}
	if err := unmarshalYAML(data, &ic); err != nil {
		return ""
	}
	if bd, ok := ic["baseDomain"].(string); ok {
		return bd
	}
	return ""
}
