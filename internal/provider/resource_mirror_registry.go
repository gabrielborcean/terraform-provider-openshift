package provider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &MirrorRegistryResource{}

type MirrorRegistryResource struct {
	providerData *ProviderData
}

func NewMirrorRegistryResource() resource.Resource {
	return &MirrorRegistryResource{}
}

func (r *MirrorRegistryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mirror_registry"
}

type MirrorRegistryModel struct {
	Hostname     types.String `tfsdk:"hostname"`
	Port         types.Int64  `tfsdk:"port"`
	StorageDir   types.String `tfsdk:"storage_dir"`
	TLSCertFile  types.String `tfsdk:"tls_cert_file"`
	TLSKeyFile   types.String `tfsdk:"tls_key_file"`
	Type         types.String `tfsdk:"type"`
	QuayRoot     types.String `tfsdk:"quay_root"`
	InitUser     types.String `tfsdk:"init_user"`
	InitPassword types.String `tfsdk:"init_password"`
	RegistryURL  types.String `tfsdk:"registry_url"`
	CACert       types.String `tfsdk:"ca_cert"`
}

func (r *MirrorRegistryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Sets up a local mirror registry for disconnected OpenShift installation.",
		Attributes: map[string]schema.Attribute{
			"hostname": schema.StringAttribute{
				Required:    true,
				Description: "FQDN of the registry host.",
			},
			"port": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(5000),
				Description: "Registry port.",
			},
			"storage_dir": schema.StringAttribute{
				Required:    true,
				Description: "Local directory for registry storage.",
			},
			"tls_cert_file": schema.StringAttribute{
				Optional:    true,
				Description: "Path to TLS certificate file (PEM). If omitted, a self-signed cert is generated.",
			},
			"tls_key_file": schema.StringAttribute{
				Optional:    true,
				Description: "Path to TLS private key file (PEM).",
			},
			"type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("quay"),
				Description: "Registry type: 'quay' (mirror-registry) or 'docker'.",
			},
			"quay_root": schema.StringAttribute{
				Optional:    true,
				Description: "Quay root installation directory (for type=quay).",
			},
			"init_user": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("admin"),
				Description: "Initial registry admin username.",
			},
			"init_password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Initial registry admin password.",
			},
			"registry_url": schema.StringAttribute{
				Computed:    true,
				Description: "Full registry URL (e.g. registry.example.com:5000).",
			},
			"ca_cert": schema.StringAttribute{
				Computed:    true,
				Description: "PEM-encoded CA certificate for the registry TLS (self-signed if generated).",
			},
		},
	}
}

func (r *MirrorRegistryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *MirrorRegistryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan MirrorRegistryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	regType := plan.Type.ValueString()
	switch regType {
	case "quay":
		if err := r.installQuayRegistry(ctx, &plan); err != nil {
			resp.Diagnostics.AddError("Installing mirror-registry", err.Error())
			return
		}
	case "docker":
		if err := r.startDockerRegistry(ctx, &plan); err != nil {
			resp.Diagnostics.AddError("Starting Docker registry", err.Error())
			return
		}
	default:
		resp.Diagnostics.AddError("Unknown registry type", fmt.Sprintf("type must be 'quay' or 'docker', got %q", regType))
		return
	}

	plan.RegistryURL = types.StringValue(fmt.Sprintf("%s:%d", plan.Hostname.ValueString(), plan.Port.ValueInt64()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MirrorRegistryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MirrorRegistryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	registryURL := fmt.Sprintf("https://%s:%d/v2/", state.Hostname.ValueString(), state.Port.ValueInt64())
	healthy := r.checkRegistryHealth(registryURL)
	if !healthy {
		resp.Diagnostics.AddWarning("Registry health check failed",
			fmt.Sprintf("Registry at %s is not responding. The resource will remain in state.", registryURL))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MirrorRegistryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan MirrorRegistryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Most changes require replacement; only init_password can be updated.
	plan.RegistryURL = types.StringValue(fmt.Sprintf("%s:%d", plan.Hostname.ValueString(), plan.Port.ValueInt64()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *MirrorRegistryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MirrorRegistryModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	regType := state.Type.ValueString()
	switch regType {
	case "quay":
		if err := r.uninstallQuayRegistry(ctx, &state); err != nil {
			resp.Diagnostics.AddError("Uninstalling mirror-registry", err.Error())
		}
	case "docker":
		if err := r.stopDockerRegistry(ctx, &state); err != nil {
			resp.Diagnostics.AddError("Stopping Docker registry", err.Error())
		}
	}
}

func (r *MirrorRegistryResource) installQuayRegistry(ctx context.Context, plan *MirrorRegistryModel) error {
	args := []string{
		"install",
		"--quayHostname", plan.Hostname.ValueString(),
		"--initUser", plan.InitUser.ValueString(),
	}

	if !plan.InitPassword.IsNull() && !plan.InitPassword.IsUnknown() {
		args = append(args, "--initPassword", plan.InitPassword.ValueString())
	}
	if !plan.QuayRoot.IsNull() && !plan.QuayRoot.IsUnknown() && plan.QuayRoot.ValueString() != "" {
		args = append(args, "--quayRoot", plan.QuayRoot.ValueString())
	}
	if !plan.StorageDir.IsNull() && !plan.StorageDir.IsUnknown() {
		args = append(args, "--quayStorage", plan.StorageDir.ValueString())
	}
	if !plan.TLSCertFile.IsNull() && !plan.TLSCertFile.IsUnknown() && plan.TLSCertFile.ValueString() != "" {
		args = append(args, "--sslCert", plan.TLSCertFile.ValueString())
	}
	if !plan.TLSKeyFile.IsNull() && !plan.TLSKeyFile.IsUnknown() && plan.TLSKeyFile.ValueString() != "" {
		args = append(args, "--sslKey", plan.TLSKeyFile.ValueString())
	}

	stdout, stderr, err := runCommand(ctx, "mirror-registry", args, nil, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("mirror-registry install failed: %w\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	// Attempt to read the generated CA cert from the default quay-rootCA.pem location
	quayRoot := "/etc/quay-install"
	if !plan.QuayRoot.IsNull() && plan.QuayRoot.ValueString() != "" {
		quayRoot = plan.QuayRoot.ValueString()
	}
	caCertPath := quayRoot + "/quay-rootCA/rootCA.pem"
	caCertData, readErr := readFileString(caCertPath)
	if readErr == nil {
		plan.CACert = types.StringValue(caCertData)
	} else {
		plan.CACert = types.StringValue("")
	}

	return nil
}

func (r *MirrorRegistryResource) uninstallQuayRegistry(ctx context.Context, state *MirrorRegistryModel) error {
	args := []string{"uninstall", "--autoApprove"}
	if !state.QuayRoot.IsNull() && state.QuayRoot.ValueString() != "" {
		args = append(args, "--quayRoot", state.QuayRoot.ValueString())
	}
	stdout, stderr, err := runCommand(ctx, "mirror-registry", args, nil, 15*time.Minute)
	if err != nil {
		return fmt.Errorf("mirror-registry uninstall failed: %w\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	return nil
}

func (r *MirrorRegistryResource) startDockerRegistry(ctx context.Context, plan *MirrorRegistryModel) error {
	port := plan.Port.ValueInt64()
	storageDir := plan.StorageDir.ValueString()
	name := fmt.Sprintf("mirror-registry-%s", plan.Hostname.ValueString())

	args := []string{
		"run", "-d",
		"--name", name,
		"--restart", "always",
		"-p", fmt.Sprintf("%d:5000", port),
		"-v", storageDir + ":/var/lib/registry",
		"registry:2",
	}

	// Use podman if available, fall back to docker
	binary := "podman"
	if _, err := findBinary("podman"); err != nil {
		binary = "docker"
	}

	stdout, stderr, err := runCommand(ctx, binary, args, nil, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("starting registry container failed: %w\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	plan.CACert = types.StringValue("")
	return nil
}

func (r *MirrorRegistryResource) stopDockerRegistry(ctx context.Context, state *MirrorRegistryModel) error {
	name := fmt.Sprintf("mirror-registry-%s", state.Hostname.ValueString())

	binary := "podman"
	if _, err := findBinary("podman"); err != nil {
		binary = "docker"
	}

	rmArgs := []string{"rm", "-f", name}
	stdout, stderr, err := runCommand(ctx, binary, rmArgs, nil, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("removing registry container failed: %w\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	return nil
}

func (r *MirrorRegistryResource) checkRegistryHealth(url string) bool {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized
}

func readFileString(path string) (string, error) {
	data, err := readFileBytes(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
