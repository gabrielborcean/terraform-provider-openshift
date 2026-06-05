package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"bytes"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// ── registration ──────────────────────────────────────────────────────────────

var _ resource.Resource = &ClusterAWSResource{}

func NewClusterAWSResource() resource.Resource { return &ClusterAWSResource{} }

type ClusterAWSResource struct {
	providerData *ProviderData
}

func (r *ClusterAWSResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_aws"
}

func (r *ClusterAWSResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	r.providerData = pd
}

// ── model ─────────────────────────────────────────────────────────────────────

type ClusterAWSModel struct {
	// Required
	ClusterName   types.String `tfsdk:"cluster_name"`
	BaseDomain    types.String `tfsdk:"base_domain"`
	Region        types.String `tfsdk:"region"`
	PullSecret    types.String `tfsdk:"pull_secret"`
	SSHPublicKey  types.String `tfsdk:"ssh_public_key"`
	AccessKeyID   types.String `tfsdk:"aws_access_key_id"`
	SecretKey     types.String `tfsdk:"aws_secret_access_key"`

	// Optional
	OpenshiftVersion         types.String `tfsdk:"openshift_version"`
	ControlPlaneInstanceType types.String `tfsdk:"control_plane_instance_type"`
	WorkerInstanceType       types.String `tfsdk:"worker_instance_type"`
	WorkerReplicas           types.Int64  `tfsdk:"worker_replicas"`
	Publish                  types.String `tfsdk:"publish"`
	FIPS                     types.Bool   `tfsdk:"fips"`
	ClusterNetworkCIDR       types.String `tfsdk:"cluster_network_cidr"`
	ServiceNetworkCIDR       types.String `tfsdk:"service_network_cidr"`
	MachineNetworkCIDR       types.String `tfsdk:"machine_network_cidr"`
	AdditionalTrustBundle    types.String `tfsdk:"additional_trust_bundle"`
	InstallDir               types.String `tfsdk:"install_dir"`
	InstallBinary            types.String `tfsdk:"install_binary"`
	InstallTimeout           types.String `tfsdk:"install_timeout"`

	// Computed
	InfraID           types.String `tfsdk:"infra_id"`
	ClusterID         types.String `tfsdk:"cluster_id"`
	APIURL            types.String `tfsdk:"api_url"`
	ConsoleURL        types.String `tfsdk:"console_url"`
	Kubeconfig        types.String `tfsdk:"kubeconfig"`
	KubeadminPassword types.String `tfsdk:"kubeadmin_password"`
}

// ── schema ────────────────────────────────────────────────────────────────────

func (r *ClusterAWSResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Creates an OpenShift cluster on AWS using openshift-install IPI. " +
			"Automatically provisions all AWS infrastructure (VPC, subnets, ELBs, Route53, EC2). " +
			"Blocks until the cluster is fully installed (~40 min).",
		Attributes: map[string]schema.Attribute{
			// ── required ──────────────────────────────────────────────────────
			"cluster_name": schema.StringAttribute{
				Required:    true,
				Description: "Name of the cluster. Used as a prefix for all AWS resources.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"base_domain": schema.StringAttribute{
				Required:    true,
				Description: "Base DNS domain. Must be a Route53 public hosted zone in the target AWS account.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"region": schema.StringAttribute{
				Required:    true,
				Description: "AWS region, e.g. us-east-1.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"pull_secret": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Red Hat pull secret JSON.",
			},
			"ssh_public_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "SSH public key for cluster nodes.",
			},
			"aws_access_key_id": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "AWS access key ID.",
			},
			"aws_secret_access_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "AWS secret access key.",
			},

			// ── optional ──────────────────────────────────────────────────────
			"openshift_version": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("4.14"),
				Description: "OpenShift version to install, e.g. '4.14'.",
			},
			"control_plane_instance_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("m6i.xlarge"),
				Description: "EC2 instance type for control plane nodes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"worker_instance_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("m6i.xlarge"),
				Description: "EC2 instance type for worker nodes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"worker_replicas": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(3),
				Description: "Number of worker nodes.",
			},
			"publish": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("External"),
				Description: "Publish strategy: External (public) or Internal (private cluster).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"fips": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Enable FIPS mode.",
				PlanModifiers: []planmodifier.Bool{},
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
			"machine_network_cidr": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("10.0.0.0/16"),
				Description: "CIDR for the machine (node) network.",
			},
			"additional_trust_bundle": schema.StringAttribute{
				Optional:    true,
				Description: "PEM-encoded CA bundle for disconnected/proxy setups.",
			},
			"install_dir": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("/install-dir"),
				Description: "Working directory for openshift-install. Must persist between apply and destroy.",
			},
			"install_binary": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("openshift-install"),
				Description: "Path to the openshift-install binary.",
			},
			"install_timeout": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("90m"),
				Description: "Timeout for the install operation (e.g. 90m, 2h).",
			},

			// ── computed ──────────────────────────────────────────────────────
			"infra_id": schema.StringAttribute{
				Computed:    true,
				Description: "AWS infrastructure ID prefix used for all provisioned resources.",
			},
			"cluster_id": schema.StringAttribute{
				Computed:    true,
				Description: "OpenShift cluster ID.",
			},
			"api_url": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster API URL.",
			},
			"console_url": schema.StringAttribute{
				Computed:    true,
				Description: "Cluster console URL.",
			},
			"kubeconfig": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "Kubeconfig for the installed cluster.",
			},
			"kubeadmin_password": schema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "kubeadmin password for the cluster console.",
			},
		},
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (r *ClusterAWSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ClusterAWSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	installDir := plan.InstallDir.ValueString()
	if err := os.MkdirAll(installDir, 0755); err != nil {
		resp.Diagnostics.AddError("Creating install dir", err.Error())
		return
	}

	// Write install-config.yaml.
	if err := writeInstallConfig(installDir, &plan); err != nil {
		resp.Diagnostics.AddError("Writing install-config.yaml", err.Error())
		return
	}

	// Run openshift-install create cluster.
	timeout, err := time.ParseDuration(plan.InstallTimeout.ValueString())
	if err != nil {
		timeout = 90 * time.Minute
	}

	binary := r.resolveInstallBinary(&plan)
	_, stderr, err := runCommand(ctx, binary,
		[]string{"create", "cluster", "--dir=" + installDir, "--log-level=info"},
		awsEnv(&plan),
		timeout,
	)
	if err != nil {
		resp.Diagnostics.AddError("Running openshift-install create cluster", stderr)
		return
	}

	if err := readInstallOutputs(installDir, &plan); err != nil {
		resp.Diagnostics.AddError("Reading install outputs", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ClusterAWSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ClusterAWSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Refresh kubeconfig from disk if still present.
	installDir := state.InstallDir.ValueString()
	kubeconfigPath := filepath.Join(installDir, "auth", "kubeconfig")
	if data, err := os.ReadFile(kubeconfigPath); err == nil {
		state.Kubeconfig = types.StringValue(string(data))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ClusterAWSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All mutable fields (worker_replicas, pull_secret, etc.) don't require
	// reinstall — just update state. Fields that do require reinstall have
	// RequiresReplace set on their plan modifier.
	var plan ClusterAWSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ClusterAWSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ClusterAWSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	installDir := state.InstallDir.ValueString()

	// openshift-install destroy cluster reads metadata.json from the install dir.
	// If the dir is gone, nothing to do.
	if _, err := os.Stat(filepath.Join(installDir, "metadata.json")); os.IsNotExist(err) {
		return
	}

	binary := r.resolveInstallBinary(&state)
	_, stderr, err := runCommand(ctx, binary,
		[]string{"destroy", "cluster", "--dir=" + installDir, "--log-level=info"},
		awsEnv(&state),
		60*time.Minute,
	)
	if err != nil {
		resp.Diagnostics.AddError("Running openshift-install destroy cluster", stderr)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (r *ClusterAWSResource) resolveInstallBinary(m *ClusterAWSModel) string {
	if b := m.InstallBinary.ValueString(); b != "" {
		return b
	}
	return "openshift-install"
}

func awsEnv(m *ClusterAWSModel) []string {
	return []string{
		"AWS_ACCESS_KEY_ID=" + m.AccessKeyID.ValueString(),
		"AWS_SECRET_ACCESS_KEY=" + m.SecretKey.ValueString(),
	}
}

// ── install-config.yaml ───────────────────────────────────────────────────────

var installConfigFuncs = template.FuncMap{
	"indent": func(spaces int, s string) string {
		pad := ""
		for i := 0; i < spaces; i++ {
			pad += " "
		}
		out := ""
		for _, line := range splitLines(s) {
			out += pad + line + "\n"
		}
		return out
	},
}

var installConfigTmpl = template.Must(template.New("ic").Funcs(installConfigFuncs).Parse(`apiVersion: v1
baseDomain: {{.BaseDomain}}
metadata:
  name: {{.ClusterName}}
platform:
  aws:
    region: {{.Region}}
    userTags:
      managed-by: terraform-provider-openshift
publish: {{.Publish}}
pullSecret: '{{.PullSecret}}'
sshKey: '{{.SSHPublicKey}}'
controlPlane:
  hyperthreading: Enabled
  name: master
  platform:
    aws:
      instanceType: {{.ControlPlaneInstanceType}}
  replicas: 3
compute:
- hyperthreading: Enabled
  name: worker
  platform:
    aws:
      instanceType: {{.WorkerInstanceType}}
  replicas: {{.WorkerReplicas}}
networking:
  networkType: OVNKubernetes
  clusterNetwork:
  - cidr: {{.ClusterNetworkCIDR}}
    hostPrefix: 23
  serviceNetwork:
  - {{.ServiceNetworkCIDR}}
  machineNetwork:
  - cidr: {{.MachineNetworkCIDR}}
{{- if .FIPS}}
fips: true
{{- end}}
{{- if .AdditionalTrustBundle}}
additionalTrustBundle: |
{{.AdditionalTrustBundle | indent 2}}
{{- end}}
`))

type installConfigVars struct {
	ClusterName              string
	BaseDomain               string
	Region                   string
	PullSecret               string
	SSHPublicKey             string
	ControlPlaneInstanceType string
	WorkerInstanceType       string
	WorkerReplicas           int64
	Publish                  string
	FIPS                     bool
	ClusterNetworkCIDR       string
	ServiceNetworkCIDR       string
	MachineNetworkCIDR       string
	AdditionalTrustBundle    string
}

func writeInstallConfig(installDir string, m *ClusterAWSModel) error {
	vars := installConfigVars{
		ClusterName:              m.ClusterName.ValueString(),
		BaseDomain:               m.BaseDomain.ValueString(),
		Region:                   m.Region.ValueString(),
		PullSecret:               m.PullSecret.ValueString(),
		SSHPublicKey:             m.SSHPublicKey.ValueString(),
		ControlPlaneInstanceType: m.ControlPlaneInstanceType.ValueString(),
		WorkerInstanceType:       m.WorkerInstanceType.ValueString(),
		WorkerReplicas:           m.WorkerReplicas.ValueInt64(),
		Publish:                  m.Publish.ValueString(),
		FIPS:                     m.FIPS.ValueBool(),
		ClusterNetworkCIDR:       m.ClusterNetworkCIDR.ValueString(),
		ServiceNetworkCIDR:       m.ServiceNetworkCIDR.ValueString(),
		MachineNetworkCIDR:       m.MachineNetworkCIDR.ValueString(),
		AdditionalTrustBundle:    m.AdditionalTrustBundle.ValueString(),
	}

	var buf bytes.Buffer
	if err := installConfigTmpl.Execute(&buf, vars); err != nil {
		return fmt.Errorf("rendering install-config.yaml: %w", err)
	}

	return os.WriteFile(filepath.Join(installDir, "install-config.yaml"), buf.Bytes(), 0600)
}

func splitLines(s string) []string {
	var lines []string
	current := ""
	for _, c := range s {
		if c == '\n' {
			lines = append(lines, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

// ── read install outputs ──────────────────────────────────────────────────────

type awsMetadata struct {
	ClusterID string `json:"clusterID"`
	InfraID   string `json:"infraID"`
}

func readInstallOutputs(installDir string, m *ClusterAWSModel) error {
	// metadata.json — cluster ID and infra ID.
	metaBytes, err := os.ReadFile(filepath.Join(installDir, "metadata.json"))
	if err != nil {
		return fmt.Errorf("reading metadata.json: %w", err)
	}
	var meta awsMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return fmt.Errorf("parsing metadata.json: %w", err)
	}
	m.ClusterID = types.StringValue(meta.ClusterID)
	m.InfraID = types.StringValue(meta.InfraID)

	// auth/kubeconfig
	kubeconfigBytes, err := os.ReadFile(filepath.Join(installDir, "auth", "kubeconfig"))
	if err != nil {
		return fmt.Errorf("reading kubeconfig: %w", err)
	}
	m.Kubeconfig = types.StringValue(string(kubeconfigBytes))

	// auth/kubeadmin-password
	passBytes, err := os.ReadFile(filepath.Join(installDir, "auth", "kubeadmin-password"))
	if err != nil {
		return fmt.Errorf("reading kubeadmin-password: %w", err)
	}
	m.KubeadminPassword = types.StringValue(string(passBytes))

	// Derive URLs from cluster name and base domain.
	name := m.ClusterName.ValueString()
	domain := m.BaseDomain.ValueString()
	m.APIURL = types.StringValue(fmt.Sprintf("https://api.%s.%s:6443", name, domain))
	m.ConsoleURL = types.StringValue(fmt.Sprintf("https://console-openshift-console.apps.%s.%s", name, domain))

	return nil
}
