package provider

// openshift_agent_iso — generates an Agent-Based Installer ISO from
// install-config.yaml and agent-config.yaml, then optionally uploads it
// to a bastion HTTP server so it can be PXE-served or written to USB.
//
// One ISO covers ALL servers — list every host's MAC address in
// agent_config so the agent knows which role to assign each machine.
// Write the ISO to a USB key once; boot each server from it in sequence.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &AgentISOResource{}

func NewAgentISOResource() resource.Resource { return &AgentISOResource{} }

type AgentISOResource struct {
	providerData *ProviderData
}

func (r *AgentISOResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_iso"
}

func (r *AgentISOResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		return
	}
	r.providerData = pd
}

// ── model ─────────────────────────────────────────────────────────────────────

type AgentISOModel struct {
	// Config inputs — pass your existing files as strings
	InstallConfig types.String `tfsdk:"install_config"`
	AgentConfig   types.String `tfsdk:"agent_config"`

	// Tooling
	InstallBinary types.String `tfsdk:"install_binary"`
	WorkDir       types.String `tfsdk:"work_dir"`

	// Optional: upload to bastion for PXE serving
	BastionHost   types.String `tfsdk:"bastion_host"`
	BastionPort   types.Int64  `tfsdk:"bastion_port"`
	BastionUser   types.String `tfsdk:"bastion_user"`
	BastionSSHKey types.String `tfsdk:"bastion_ssh_key"`
	BastionISODir types.String `tfsdk:"bastion_iso_dir"`

	// Computed
	ISOPath    types.String `tfsdk:"iso_path"`
	ISOSize    types.Int64  `tfsdk:"iso_size_bytes"`
	Checksum   types.String `tfsdk:"checksum_sha256"`
	BastionURL types.String `tfsdk:"bastion_iso_url"`
}

// ── schema ────────────────────────────────────────────────────────────────────

func (r *AgentISOResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Generates an OpenShift Agent-Based Installer ISO from your existing " +
			"install-config.yaml and agent-config.yaml. One ISO boots all servers — " +
			"list every host's MAC address and role in agent_config. " +
			"Write to USB once, boot each server sequentially. " +
			"Optionally uploads the ISO to a bastion host for PXE serving via openshift_pxe_server.",
		Attributes: map[string]schema.Attribute{
			// ── inputs ────────────────────────────────────────────────────────
			"install_config": schema.StringAttribute{
				Required: true,
				Description: "Contents of install-config.yaml. " +
					"Tip: use file() to read your existing config: install_config = file(\"install-config.yaml\")",
			},
			"agent_config": schema.StringAttribute{
				Required: true,
				Description: "Contents of agent-config.yaml. List ALL hosts with their MAC addresses and roles " +
					"(master/worker). The same ISO will correctly configure each server based on its MAC. " +
					"Tip: agent_config = file(\"agent-config.yaml\")",
			},
			"install_binary": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("openshift-install"),
				Description: "Path to the openshift-install binary.",
			},
			"work_dir": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("/install-dir/agent-iso"),
				Description: "Working directory for openshift-install agent create image.",
			},

			// ── optional bastion upload ───────────────────────────────────────
			"bastion_host": schema.StringAttribute{
				Optional:    true,
				Description: "Bastion host to upload the ISO to (for PXE serving). Leave empty to keep ISO local only.",
			},
			"bastion_port": schema.Int64Attribute{
				Optional:    true,
				Description: "SSH port on the bastion host. Defaults to 22.",
			},
			"bastion_user": schema.StringAttribute{
				Optional:    true,
				Description: "SSH user on the bastion host. Defaults to core.",
			},
			"bastion_ssh_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "PEM-encoded SSH private key for bastion upload.",
			},
			"bastion_iso_dir": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("/opt/pxe-server/http"),
				Description: "Directory on the bastion to upload the ISO to.",
			},

			// ── computed ──────────────────────────────────────────────────────
			"iso_path": schema.StringAttribute{
				Computed:    true,
				Description: "Local path to the generated ISO file.",
			},
			"iso_size_bytes": schema.Int64Attribute{
				Computed:    true,
				Description: "Size of the generated ISO in bytes.",
			},
			"checksum_sha256": schema.StringAttribute{
				Computed:    true,
				Description: "SHA256 checksum of the ISO. Verify after writing to USB: sha256sum /dev/sdX",
			},
			"bastion_iso_url": schema.StringAttribute{
				Computed:    true,
				Description: "HTTP URL of the ISO on the bastion (set when bastion_host is provided). Use as iso_path in openshift_pxe_server.",
			},
		},
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (r *AgentISOResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AgentISOModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workDir := plan.WorkDir.ValueString()
	if err := os.MkdirAll(workDir, 0755); err != nil {
		resp.Diagnostics.AddError("Creating work dir", err.Error())
		return
	}

	// Write config files — openshift-install agent create image reads these.
	if err := os.WriteFile(filepath.Join(workDir, "install-config.yaml"),
		[]byte(plan.InstallConfig.ValueString()), 0600); err != nil {
		resp.Diagnostics.AddError("Writing install-config.yaml", err.Error())
		return
	}
	if err := os.WriteFile(filepath.Join(workDir, "agent-config.yaml"),
		[]byte(plan.AgentConfig.ValueString()), 0600); err != nil {
		resp.Diagnostics.AddError("Writing agent-config.yaml", err.Error())
		return
	}

	// Generate the ISO.
	binary := plan.InstallBinary.ValueString()
	_, stderr, err := runCommand(ctx, binary,
		[]string{"agent", "create", "image", "--dir=" + workDir, "--log-level=info"},
		nil,
		30*time.Minute,
	)
	if err != nil {
		resp.Diagnostics.AddError("Running openshift-install agent create image", stderr)
		return
	}

	// Find the generated ISO.
	isoPath := filepath.Join(workDir, "agent.x86_64.iso")
	if _, err := os.Stat(isoPath); os.IsNotExist(err) {
		// Try arm64.
		isoPath = filepath.Join(workDir, "agent.aarch64.iso")
	}

	info, err := os.Stat(isoPath)
	if err != nil {
		resp.Diagnostics.AddError("Finding generated ISO", fmt.Sprintf("expected at %s: %s", isoPath, err.Error()))
		return
	}

	// Checksum.
	checksum, err := sha256File(isoPath)
	if err != nil {
		resp.Diagnostics.AddError("Computing ISO checksum", err.Error())
		return
	}

	plan.ISOPath = types.StringValue(isoPath)
	plan.ISOSize = types.Int64Value(info.Size())
	plan.Checksum = types.StringValue(checksum)
	plan.BastionURL = types.StringNull()

	// Optional bastion upload.
	if !plan.BastionHost.IsNull() && plan.BastionHost.ValueString() != "" {
		url, uploadErr := r.uploadToBastionHTTP(&plan, isoPath)
		if uploadErr != nil {
			resp.Diagnostics.AddError("Uploading ISO to bastion", uploadErr.Error())
			return
		}
		plan.BastionURL = types.StringValue(url)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AgentISOResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AgentISOModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)

	// If the ISO file is gone, signal drift.
	if _, err := os.Stat(state.ISOPath.ValueString()); os.IsNotExist(err) {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AgentISOResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Regenerate on any config change.
	r.Create(ctx, resource.CreateRequest{Config: req.Config, Plan: req.Plan, ProviderMeta: req.ProviderMeta}, (*resource.CreateResponse)(resp))
}

func (r *AgentISOResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AgentISOModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	// Remove generated files — leave work_dir itself in case user wants logs.
	os.Remove(state.ISOPath.ValueString()) //nolint:errcheck
}

// ── upload via SCP-over-SSH ───────────────────────────────────────────────────

func (r *AgentISOResource) uploadToBastionHTTP(plan *AgentISOModel, isoPath string) (string, error) {
	port := int64(22)
	if !plan.BastionPort.IsNull() {
		port = plan.BastionPort.ValueInt64()
	}
	user := "core"
	if !plan.BastionUser.IsNull() && plan.BastionUser.ValueString() != "" {
		user = plan.BastionUser.ValueString()
	}

	client, err := sshConnect(&AssistedServiceModel{
		BastionHost:   plan.BastionHost,
		BastionPort:   types.Int64Value(port),
		BastionUser:   types.StringValue(user),
		BastionSSHKey: plan.BastionSSHKey,
	})
	if err != nil {
		return "", fmt.Errorf("SSH connect for upload: %w", err)
	}
	defer client.Close()

	destDir := plan.BastionISODir.ValueString()
	destFile := filepath.Join(destDir, "agent.iso")

	// Create dest dir.
	if _, err := sshRun(client, fmt.Sprintf("mkdir -p %s", destDir)); err != nil {
		return "", fmt.Errorf("creating %s on bastion: %w", destDir, err)
	}

	// Upload via SSH session stdin → cat > destFile.
	isoData, err := os.ReadFile(isoPath)
	if err != nil {
		return "", fmt.Errorf("reading ISO: %w", err)
	}

	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session: %w", err)
	}
	defer sess.Close()

	sess.Stdin = bytesReader(isoData)
	if err := sess.Run(fmt.Sprintf("cat > %s", destFile)); err != nil {
		return "", fmt.Errorf("uploading ISO: %w", err)
	}

	return fmt.Sprintf("http://%s:8080/agent.iso", plan.BastionHost.ValueString()), nil
}
