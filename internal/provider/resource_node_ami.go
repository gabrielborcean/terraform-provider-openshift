package provider

// resource_node_ami.go — builds a custom RHCOS AMI via Packer.
//
// Flow:
//   1. Resolve the base RHCOS AMI for the target region from the OCP release
//      stream JSON (openshift-install coreos print-stream-json).
//   2. Generate a temporary SSH keypair for Packer to SSH into the builder instance.
//   3. Write a Packer HCL file to work_dir.
//   4. Execute `packer build`, parse the AMI ID from stdout.
//   5. Store the AMI ID in state so openshift_cluster_aws / openshift_machine_set
//      can reference it.
//
// Delete deregisters the AMI and deletes its backing snapshot.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/crypto/ssh"
)

// ── registration ──────────────────────────────────────────────────────────────

var _ resource.Resource = &NodeAMIResource{}

func NewNodeAMIResource() resource.Resource { return &NodeAMIResource{} }

type NodeAMIResource struct{}

func (r *NodeAMIResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node_ami"
}

func (r *NodeAMIResource) Configure(_ context.Context, _ resource.ConfigureRequest, _ resource.ConfigureResponse) {
}

// ── model ─────────────────────────────────────────────────────────────────────

type NodeAMIModel struct {
	// Required
	Region      types.String `tfsdk:"region"`
	OcpVersion  types.String `tfsdk:"ocp_version"`
	PullSecret  types.String `tfsdk:"pull_secret"`
	AccessKeyID types.String `tfsdk:"aws_access_key_id"`
	SecretKey   types.String `tfsdk:"aws_secret_access_key"`

	// Optional
	BaseAMI       types.String `tfsdk:"base_ami"`        // auto-resolved if empty
	InstanceType  types.String `tfsdk:"instance_type"`   // builder EC2 type
	AMIName       types.String `tfsdk:"ami_name"`        // defaults to ocp-node-<version>-<ts>
	RegistryURL   types.String `tfsdk:"registry_url"`    // airgapped mirror; empty = use pull_secret
	WorkDir       types.String `tfsdk:"work_dir"`
	PackerBinary  types.String `tfsdk:"packer_binary"`
	Architecture  types.String `tfsdk:"architecture"`    // x86_64 or aarch64
	ExtraPackages types.String `tfsdk:"extra_packages"`  // space-separated RPM list

	// Computed
	AMIID      types.String `tfsdk:"ami_id"`
	SnapshotID types.String `tfsdk:"snapshot_id"`
	BaseAMIResolved types.String `tfsdk:"base_ami_resolved"`
}

// ── schema ────────────────────────────────────────────────────────────────────

func (r *NodeAMIResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Builds a custom RHCOS AMI via Packer with container images pre-pulled. " +
			"Use the output ami_id in openshift_cluster_aws or openshift_machine_set to roll " +
			"nodes without touching a registry at boot time. Ideal for airgapped deployments " +
			"or blue-green node pool upgrades.",
		Attributes: map[string]schema.Attribute{
			// ── required ──────────────────────────────────────────────────────
			"region": schema.StringAttribute{
				Required:    true,
				Description: "AWS region to build and register the AMI in.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ocp_version": schema.StringAttribute{
				Required:    true,
				Description: "OpenShift version, e.g. '4.14'. Used to resolve the correct RHCOS base AMI.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"pull_secret": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "Red Hat pull secret JSON. Used to authenticate image pulls during the Packer build.",
			},
			"aws_access_key_id": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "AWS access key ID for Packer to launch the builder instance and register the AMI.",
			},
			"aws_secret_access_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "AWS secret access key.",
			},

			// ── optional ──────────────────────────────────────────────────────
			"base_ami": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Base RHCOS AMI ID. Auto-resolved from ocp_version + region via openshift-install if not set.",
			},
			"instance_type": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("m5.xlarge"),
				Description: "EC2 instance type for the Packer builder. Needs enough disk for image pre-pull.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"ami_name": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Description: "Name for the resulting AMI. Defaults to ocp-node-<ocp_version>-<unix_timestamp>.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"registry_url": schema.StringAttribute{
				Optional:    true,
				Description: "Mirror registry URL for airgapped builds (e.g. bastion.example.internal:8443). " +
					"If set, images are pulled from here instead of quay.io.",
			},
			"work_dir": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("/install-dir/packer"),
				Description: "Local directory where Packer HCL and SSH keys are written.",
			},
			"packer_binary": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("packer"),
				Description: "Path to the packer binary.",
			},
			"architecture": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("x86_64"),
				Description: "CPU architecture: x86_64 or aarch64.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"extra_packages": schema.StringAttribute{
				Optional:    true,
				Description: "Space-separated list of additional RPM packages to install into the AMI.",
			},

			// ── computed ──────────────────────────────────────────────────────
			"ami_id": schema.StringAttribute{
				Computed:    true,
				Description: "ID of the resulting AMI. Pass to openshift_cluster_aws or openshift_machine_set.",
			},
			"snapshot_id": schema.StringAttribute{
				Computed:    true,
				Description: "EBS snapshot ID backing the AMI. Deleted when the resource is destroyed.",
			},
			"base_ami_resolved": schema.StringAttribute{
				Computed:    true,
				Description: "The RHCOS base AMI that was used (resolved or passed in).",
			},
		},
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (r *NodeAMIResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan NodeAMIModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workDir := plan.WorkDir.ValueString()
	if err := os.MkdirAll(workDir, 0755); err != nil {
		resp.Diagnostics.AddError("Creating work_dir", err.Error())
		return
	}

	// 1. Resolve base RHCOS AMI.
	baseAMI := plan.BaseAMI.ValueString()
	if baseAMI == "" {
		var err error
		baseAMI, err = resolveRHCOSAMI(ctx, plan.OcpVersion.ValueString(), plan.Region.ValueString(), plan.Architecture.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Resolving RHCOS base AMI", err.Error())
			return
		}
	}
	plan.BaseAMI = types.StringValue(baseAMI)
	plan.BaseAMIResolved = types.StringValue(baseAMI)

	// 2. Default AMI name.
	amiName := plan.AMIName.ValueString()
	if amiName == "" {
		amiName = fmt.Sprintf("ocp-node-%s-%d", plan.OcpVersion.ValueString(), time.Now().Unix())
		plan.AMIName = types.StringValue(amiName)
	}

	// 3. Generate temporary SSH keypair for Packer.
	privKeyPath, pubKey, err := generateSSHKeypair(workDir)
	if err != nil {
		resp.Diagnostics.AddError("Generating SSH keypair for Packer", err.Error())
		return
	}
	defer os.Remove(privKeyPath)

	// 4. Write pull-secret for use inside the builder.
	pullSecretPath := filepath.Join(workDir, "pull-secret.json")
	if err := os.WriteFile(pullSecretPath, []byte(plan.PullSecret.ValueString()), 0600); err != nil {
		resp.Diagnostics.AddError("Writing pull-secret.json", err.Error())
		return
	}
	defer os.Remove(pullSecretPath)

	// 5. Generate Packer HCL.
	hclPath := filepath.Join(workDir, "ocp-node.pkr.hcl")
	if err := writePackerHCL(hclPath, packerVars{
		Region:        plan.Region.ValueString(),
		BaseAMI:       baseAMI,
		InstanceType:  plan.InstanceType.ValueString(),
		AMIName:       amiName,
		SSHPrivKey:    privKeyPath,
		SSHPubKey:     pubKey,
		PullSecretPath: pullSecretPath,
		RegistryURL:   plan.RegistryURL.ValueString(),
		OcpVersion:    plan.OcpVersion.ValueString(),
		ExtraPackages: plan.ExtraPackages.ValueString(),
	}); err != nil {
		resp.Diagnostics.AddError("Writing Packer HCL", err.Error())
		return
	}

	// 6. packer init (downloads amazon plugin).
	packerBin := plan.PackerBinary.ValueString()
	if _, _, err := runCommand(ctx, packerBin,
		[]string{"init", hclPath},
		nodeAMIEnv(&plan),
		5*time.Minute,
	); err != nil {
		resp.Diagnostics.AddError("packer init failed", err.Error())
		return
	}

	// 7. packer build.
	stdout, stderr, err := runCommand(ctx, packerBin,
		[]string{"build", "-machine-readable", hclPath},
		nodeAMIEnv(&plan),
		60*time.Minute,
	)
	if err != nil {
		resp.Diagnostics.AddError("packer build failed", stderr)
		return
	}

	// 8. Parse AMI ID and snapshot ID from machine-readable output.
	amiID, snapshotID, err := parsePackerOutput(stdout)
	if err != nil {
		resp.Diagnostics.AddError("Parsing packer output", fmt.Sprintf("%s\n\nstdout:\n%s", err.Error(), stdout))
		return
	}

	plan.AMIID = types.StringValue(amiID)
	plan.SnapshotID = types.StringValue(snapshotID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *NodeAMIResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state NodeAMIModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if AMI still exists via AWS CLI.
	amiID := state.AMIID.ValueString()
	if amiID != "" {
		exists, err := amiExists(ctx, amiID, state.Region.ValueString(), nodeAMIEnv(&state))
		if err == nil && !exists {
			// AMI was deleted outside Terraform — remove from state.
			resp.State.RemoveResource(ctx)
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *NodeAMIResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
	// All meaningful fields have RequiresReplace. Nothing to update in-place.
}

func (r *NodeAMIResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state NodeAMIModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	amiID := state.AMIID.ValueString()
	if amiID == "" {
		return
	}

	env := nodeAMIEnv(&state)
	region := state.Region.ValueString()

	// Deregister the AMI.
	if _, _, err := runCommand(ctx, "aws",
		[]string{"ec2", "deregister-image", "--image-id", amiID, "--region", region},
		env, 2*time.Minute,
	); err != nil {
		resp.Diagnostics.AddWarning("Deregistering AMI", fmt.Sprintf("AMI %s may need manual cleanup: %s", amiID, err.Error()))
	}

	// Delete the backing snapshot.
	if snap := state.SnapshotID.ValueString(); snap != "" {
		if _, _, err := runCommand(ctx, "aws",
			[]string{"ec2", "delete-snapshot", "--snapshot-id", snap, "--region", region},
			env, 2*time.Minute,
		); err != nil {
			resp.Diagnostics.AddWarning("Deleting snapshot", fmt.Sprintf("Snapshot %s may need manual cleanup: %s", snap, err.Error()))
		}
	}
}

// ── RHCOS AMI resolution ──────────────────────────────────────────────────────

// rhcosStream is the subset of `openshift-install coreos print-stream-json` we need.
type rhcosStream struct {
	Architectures map[string]struct {
		Images struct {
			AWS struct {
				Regions map[string]struct {
					Image string `json:"image"`
				} `json:"regions"`
			} `json:"aws"`
		} `json:"images"`
	} `json:"architectures"`
}

// resolveRHCOSAMI runs `openshift-install coreos print-stream-json` and extracts
// the RHCOS AMI ID for the given region and architecture.
func resolveRHCOSAMI(ctx context.Context, ocpVersion, region, arch string) (string, error) {
	// openshift-install bundles the RHCOS stream JSON for its release.
	// It doesn't need an install-config or working directory.
	cmd := exec.CommandContext(ctx, "openshift-install", "coreos", "print-stream-json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("openshift-install coreos print-stream-json: %w\nstderr: %s", err, stderr.String())
	}

	var stream rhcosStream
	if err := json.Unmarshal(stdout.Bytes(), &stream); err != nil {
		return "", fmt.Errorf("parsing stream JSON: %w", err)
	}

	// Normalize arch: aarch64 stays as-is, x86_64 stays as-is.
	archData, ok := stream.Architectures[arch]
	if !ok {
		// Try x86_64 as fallback.
		archData, ok = stream.Architectures["x86_64"]
		if !ok {
			return "", fmt.Errorf("architecture %q not found in RHCOS stream JSON", arch)
		}
	}

	regionData, ok := archData.Images.AWS.Regions[region]
	if !ok {
		return "", fmt.Errorf("region %q not found in RHCOS stream JSON for architecture %q. "+
			"Available regions: %s", region, arch, strings.Join(availableRegions(archData.Images.AWS.Regions), ", "))
	}

	if regionData.Image == "" {
		return "", fmt.Errorf("empty AMI ID for region %q in RHCOS stream JSON", region)
	}

	return regionData.Image, nil
}

func availableRegions(m map[string]struct {
	Image string `json:"image"`
}) []string {
	regions := make([]string, 0, len(m))
	for r := range m {
		regions = append(regions, r)
	}
	return regions
}

// ── Packer HCL generation ─────────────────────────────────────────────────────

type packerVars struct {
	Region         string
	BaseAMI        string
	InstanceType   string
	AMIName        string
	SSHPrivKey     string
	SSHPubKey      string
	PullSecretPath string
	RegistryURL    string
	OcpVersion     string
	ExtraPackages  string
}

var packerHCLTmpl = template.Must(template.New("packer").Parse(`
packer {
  required_plugins {
    amazon = {
      version = ">= 1.3.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

# ── Builder instance ──────────────────────────────────────────────────────────
source "amazon-ebs" "rhcos" {
  region        = "{{.Region}}"
  source_ami    = "{{.BaseAMI}}"
  instance_type = "{{.InstanceType}}"
  ami_name      = "{{.AMIName}}"

  # RHCOS uses Ignition for init. We inject an SSH key via user_data so
  # Packer can connect as the 'core' user.
  user_data = jsonencode({
    ignition = {
      version = "3.4.0"
    }
    passwd = {
      users = [
        {
          name              = "core"
          sshAuthorizedKeys = ["{{.SSHPubKey}}"]
        }
      ]
    }
  })

  ssh_username         = "core"
  ssh_private_key_file = "{{.SSHPrivKey}}"
  ssh_timeout          = "10m"

  # Root volume — needs space for pre-pulled images (~30 GB per OCP release).
  launch_block_device_mappings {
    device_name           = "/dev/xvda"
    volume_size           = 120
    volume_type           = "gp3"
    delete_on_termination = true
  }

  tags = {
    Name        = "{{.AMIName}}"
    OCP_VERSION = "{{.OcpVersion}}"
    built_by    = "terraform-provider-openshift"
  }
}

# ── Build steps ───────────────────────────────────────────────────────────────
build {
  sources = ["source.amazon-ebs.rhcos"]

  # Upload pull secret for authenticated image pulls.
  provisioner "file" {
    source      = "{{.PullSecretPath}}"
    destination = "/tmp/pull-secret.json"
  }

  provisioner "shell" {
    inline = [
      # Wait for cloud-init / Ignition to settle.
      "sudo systemctl is-system-running --wait || true",

      # Pre-pull core OCP release images.
      # oc-mirror or skopeo copies from the mirror registry (airgapped) or quay.io (connected).
      {{- if .RegistryURL}}
      "sudo podman pull --authfile /tmp/pull-secret.json {{.RegistryURL}}/openshift-release-dev/ocp-release:{{.OcpVersion}} || true",
      "sudo podman pull --authfile /tmp/pull-secret.json {{.RegistryURL}}/openshift-release-dev/ocp-v4.0-art-dev:{{.OcpVersion}} || true",
      {{- else}}
      "sudo podman pull --authfile /tmp/pull-secret.json quay.io/openshift-release-dev/ocp-release:{{.OcpVersion}} || true",
      {{- end}}

      {{- if .ExtraPackages}}
      # Install extra RPM packages (layered RHCOS).
      "sudo rpm-ostree install {{.ExtraPackages}}",
      "sudo rpm-ostree finalize-deployment || true",
      {{- end}}

      # Clean up pull secret from disk before snapshot.
      "rm -f /tmp/pull-secret.json",

      # Sync filesystem writes to disk.
      "sudo sync",
    ]
  }
}
`))

func writePackerHCL(path string, vars packerVars) error {
	var buf bytes.Buffer
	if err := packerHCLTmpl.Execute(&buf, vars); err != nil {
		return fmt.Errorf("rendering packer HCL: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0600)
}

// ── SSH keypair generation ────────────────────────────────────────────────────

// generateSSHKeypair creates a temporary RSA keypair in workDir.
// Returns the path to the private key file and the authorized_keys-format public key.
func generateSSHKeypair(workDir string) (privKeyPath string, pubKey string, err error) {
	privKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", fmt.Errorf("generating RSA key: %w", err)
	}

	// PEM-encode private key.
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})

	privKeyPath = filepath.Join(workDir, "packer_rsa")
	if err := os.WriteFile(privKeyPath, privPEM, 0600); err != nil {
		return "", "", fmt.Errorf("writing private key: %w", err)
	}

	// Generate authorized_keys format public key.
	pub, err := ssh.NewPublicKey(&privKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("creating SSH public key: %w", err)
	}
	pubKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

	return privKeyPath, pubKey, nil
}

// ── Packer output parsing ─────────────────────────────────────────────────────

// parsePackerOutput extracts the AMI ID and snapshot ID from packer's
// machine-readable output format:
//   <timestamp>,<builder>,artifact,0,id,<region>:<ami-id>
//   <timestamp>,<builder>,artifact,0,string,AMIs were created: ...
var (
	reAMIID      = regexp.MustCompile(`artifact,0,id,\S+:(ami-[a-f0-9]+)`)
	reSnapshotID = regexp.MustCompile(`(snap-[a-f0-9]+)`)
)

func parsePackerOutput(stdout string) (amiID, snapshotID string, err error) {
	for _, line := range strings.Split(stdout, "\n") {
		if m := reAMIID.FindStringSubmatch(line); m != nil {
			amiID = m[1]
		}
		if strings.Contains(line, "snapshot") {
			if m := reSnapshotID.FindStringSubmatch(line); m != nil {
				snapshotID = m[1]
			}
		}
	}
	if amiID == "" {
		return "", "", fmt.Errorf("AMI ID not found in packer output")
	}
	return amiID, snapshotID, nil
}

// ── AMI existence check ───────────────────────────────────────────────────────

func amiExists(ctx context.Context, amiID, region string, env []string) (bool, error) {
	stdout, _, err := runCommand(ctx, "aws",
		[]string{"ec2", "describe-images", "--image-ids", amiID, "--region", region, "--output", "json"},
		env, 30*time.Second,
	)
	if err != nil {
		// AWS CLI exits non-zero if AMI doesn't exist.
		return false, nil
	}
	return strings.Contains(stdout, amiID), nil
}

// ── env helpers ───────────────────────────────────────────────────────────────

func nodeAMIEnv(m *NodeAMIModel) []string {
	return []string{
		"AWS_ACCESS_KEY_ID=" + m.AccessKeyID.ValueString(),
		"AWS_SECRET_ACCESS_KEY=" + m.SecretKey.ValueString(),
		"AWS_DEFAULT_REGION=" + m.Region.ValueString(),
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
}
