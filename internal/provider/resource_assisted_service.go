package provider

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/crypto/ssh"
)

// ── resource registration ─────────────────────────────────────────────────────

var _ resource.Resource = &AssistedServiceResource{}

func NewAssistedServiceResource() resource.Resource {
	return &AssistedServiceResource{}
}

type AssistedServiceResource struct{}

func (r *AssistedServiceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_assisted_service"
}

// ── model ─────────────────────────────────────────────────────────────────────

type AssistedServiceModel struct {
	// SSH connection
	BastionHost   types.String `tfsdk:"bastion_host"`
	BastionPort   types.Int64  `tfsdk:"bastion_port"`
	BastionUser   types.String `tfsdk:"bastion_user"`
	BastionSSHKey types.String `tfsdk:"bastion_ssh_key"`

	// Service config
	ServiceBaseURL    types.String `tfsdk:"service_base_url"`
	OCPVersions       types.String `tfsdk:"ocp_versions"`
	MirrorRegistryURL types.String `tfsdk:"mirror_registry_url"`
	MirrorRegistryCA  types.String `tfsdk:"mirror_registry_ca"`
	HTTPProxy         types.String `tfsdk:"http_proxy"`
	HTTPSProxy        types.String `tfsdk:"https_proxy"`
	NoProxy           types.String `tfsdk:"no_proxy"`

	// Computed
	APIURL types.String `tfsdk:"api_url"`
}

// ── schema ────────────────────────────────────────────────────────────────────

func (r *AssistedServiceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Deploys the Assisted Installer service on a bastion host via SSH using podman. " +
			"Use the api_url output as assisted_service_url in the openshift provider or openshift_cluster resource.",
		Attributes: map[string]schema.Attribute{
			"bastion_host": schema.StringAttribute{
				Required:    true,
				Description: "Hostname or IP of the bastion host.",
			},
			"bastion_port": schema.Int64Attribute{
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(22),
				Description: "SSH port on the bastion host.",
			},
			"bastion_user": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("core"),
				Description: "SSH user on the bastion host. Defaults to 'core' (RHCOS/Fedora CoreOS).",
			},
			"bastion_ssh_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "PEM-encoded SSH private key for connecting to the bastion host.",
			},
			"service_base_url": schema.StringAttribute{
				Required:    true,
				Description: "URL at which the Assisted Service will be reachable from the nodes, e.g. http://bastion.example.internal:8090.",
			},
			"ocp_versions": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(`{"4.14":{"display_name":"4.14","release_version":"4.14","release_image":"","rhcos_image":"","rhcos_rootfs":"","rhcos_version":"","support_level":"production"}}`),
				Description: "JSON map of supported OCP versions passed to the Assisted Service.",
			},
			"mirror_registry_url": schema.StringAttribute{
				Optional:    true,
				Description: "Mirror registry URL for airgapped deployments, e.g. bastion.example.internal:8443.",
			},
			"mirror_registry_ca": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "PEM-encoded CA certificate for the mirror registry.",
			},
			"http_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "HTTP proxy for the Assisted Service to reach the internet (if needed).",
			},
			"https_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "HTTPS proxy for the Assisted Service.",
			},
			"no_proxy": schema.StringAttribute{
				Optional:    true,
				Description: "Comma-separated list of hosts that bypass the proxy.",
			},
			"api_url": schema.StringAttribute{
				Computed:    true,
				Description: "Base URL of the deployed Assisted Installer API. Use as assisted_service_url in the provider or openshift_cluster.",
			},
		},
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (r *AssistedServiceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AssistedServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := sshConnect(&plan)
	if err != nil {
		resp.Diagnostics.AddError("SSH connect", err.Error())
		return
	}
	defer client.Close()

	script, err := renderDeployScript(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Rendering deploy script", err.Error())
		return
	}

	if out, runErr := sshRun(client, script); runErr != nil {
		resp.Diagnostics.AddError("Deploying assisted-service", fmt.Sprintf("%s\n%s", runErr.Error(), out))
		return
	}

	if err := waitForService(ctx, plan.ServiceBaseURL.ValueString(), 5*time.Minute); err != nil {
		resp.Diagnostics.AddError("Waiting for assisted-service health check", err.Error())
		return
	}

	plan.APIURL = plan.ServiceBaseURL
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AssistedServiceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AssistedServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := sshConnect(&state)
	if err != nil {
		// Can't reach bastion — leave state as-is.
		return
	}
	defer client.Close()

	out, _ := sshRun(client, "podman pod exists assisted-service && echo running || echo stopped")
	if strings.TrimSpace(out) == "stopped" {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AssistedServiceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AssistedServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := sshConnect(&plan)
	if err != nil {
		resp.Diagnostics.AddError("SSH connect", err.Error())
		return
	}
	defer client.Close()

	// Tear down and redeploy.
	sshRun(client, "podman pod stop assisted-service 2>/dev/null; podman pod rm -f assisted-service 2>/dev/null || true") //nolint:errcheck

	script, err := renderDeployScript(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Rendering deploy script", err.Error())
		return
	}

	if out, runErr := sshRun(client, script); runErr != nil {
		resp.Diagnostics.AddError("Redeploying assisted-service", fmt.Sprintf("%s\n%s", runErr.Error(), out))
		return
	}

	if err := waitForService(ctx, plan.ServiceBaseURL.ValueString(), 5*time.Minute); err != nil {
		resp.Diagnostics.AddError("Waiting for assisted-service health check", err.Error())
		return
	}

	plan.APIURL = plan.ServiceBaseURL
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AssistedServiceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AssistedServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := sshConnect(&state)
	if err != nil {
		// If we can't reach the bastion, consider it gone.
		return
	}
	defer client.Close()

	sshRun(client, "podman pod stop assisted-service 2>/dev/null; podman pod rm -f assisted-service 2>/dev/null || true") //nolint:errcheck
}

// ── SSH helpers ───────────────────────────────────────────────────────────────

func sshConnect(m *AssistedServiceModel) (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(m.BastionSSHKey.ValueString()))
	if err != nil {
		return nil, fmt.Errorf("parsing SSH private key: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User:            m.BastionUser.ValueString(),
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         30 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", m.BastionHost.ValueString(), m.BastionPort.ValueInt64())
	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", addr, err)
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// sshRun sends a script over stdin to bash on the remote host.
func sshRun(client *ssh.Client, script string) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()

	var buf bytes.Buffer
	sess.Stdout = &buf
	sess.Stderr = &buf
	sess.Stdin = strings.NewReader(script)

	if err := sess.Run("bash -s"); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

// ── deploy script template ────────────────────────────────────────────────────

var deployScriptTmpl = template.Must(template.New("deploy").Parse(`#!/usr/bin/env bash
set -euo pipefail

SERVICE_DIR=/opt/assisted-service
mkdir -p "${SERVICE_DIR}"

# ── write mirror CA if provided ───────────────────────────────────────────────
{{- if .MirrorCA}}
mkdir -p /etc/pki/ca-trust/source/anchors
cat > /etc/pki/ca-trust/source/anchors/mirror-registry.crt <<'PEMEOF'
{{.MirrorCA}}
PEMEOF
update-ca-trust extract
{{- end}}

# ── write environment file ────────────────────────────────────────────────────
cat > "${SERVICE_DIR}/assisted.env" <<'EOF'
SERVICE_BASE_URL={{.ServiceBaseURL}}
DEPLOY_TARGET=onprem
OPENSHIFT_VERSIONS={{.OCPVersions}}
{{- if .MirrorRegistryURL}}
MIRROR_REGISTRY_URL={{.MirrorRegistryURL}}
{{- end}}
{{- if .HTTPProxy}}
HTTP_PROXY={{.HTTPProxy}}
HTTPS_PROXY={{.HTTPSProxy}}
NO_PROXY={{.NoProxy}}
{{- end}}
EOF

# ── pod YAML ──────────────────────────────────────────────────────────────────
cat > "${SERVICE_DIR}/pod.yaml" <<'PODEOF'
apiVersion: v1
kind: Pod
metadata:
  name: assisted-service
spec:
  restartPolicy: Always
  containers:

  - name: db
    image: quay.io/centos7/postgresql-12-centos7:latest
    env:
    - name: POSTGRESQL_DATABASE
      value: installer
    - name: POSTGRESQL_USER
      value: admin
    - name: POSTGRESQL_PASSWORD
      value: admin
    volumeMounts:
    - name: db-data
      mountPath: /var/lib/pgsql/data

  - name: service
    image: quay.io/edge-infrastructure/assisted-service:latest
    ports:
    - containerPort: 8090
      hostPort: 8090
    envFrom:
    - configMapRef:
        name: assisted-service-config
    env:
    - name: DB_HOST
      value: localhost
    - name: DB_PORT
      value: "5432"
    - name: DB_NAME
      value: installer
    - name: DB_USER
      value: admin
    - name: DB_PASS
      value: admin

  - name: image-service
    image: quay.io/edge-infrastructure/assisted-image-service:latest
    ports:
    - containerPort: 8888
      hostPort: 8888
    env:
    - name: ASSISTED_SERVICE_SCHEME
      value: http
    - name: ASSISTED_SERVICE_HOST
      value: localhost:8090
    - name: IMAGE_SERVICE_BASE_URL
      value: {{.ServiceBaseURL}}:8888
    - name: LISTEN_PORT
      value: "8888"

  volumes:
  - name: db-data
    emptyDir: {}
PODEOF

# ── configmap ─────────────────────────────────────────────────────────────────
cat > "${SERVICE_DIR}/configmap.yaml" <<CMEOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: assisted-service-config
data:
$(while IFS='=' read -r k v; do
  [[ -z "$k" || "$k" == \#* ]] && continue
  printf "  %s: \"%s\"\n" "$k" "$v"
done < "${SERVICE_DIR}/assisted.env")
CMEOF

# ── stop existing pod if running ──────────────────────────────────────────────
podman pod exists assisted-service 2>/dev/null && \
  podman pod stop assisted-service && \
  podman pod rm -f assisted-service || true

# ── start ─────────────────────────────────────────────────────────────────────
podman kube play "${SERVICE_DIR}/pod.yaml"

echo "assisted-service started"
`))

type deployScriptVars struct {
	ServiceBaseURL    string
	OCPVersions       string
	MirrorRegistryURL string
	MirrorCA          string
	HTTPProxy         string
	HTTPSProxy        string
	NoProxy           string
}

func renderDeployScript(m *AssistedServiceModel) (string, error) {
	vars := deployScriptVars{
		ServiceBaseURL:    m.ServiceBaseURL.ValueString(),
		OCPVersions:       m.OCPVersions.ValueString(),
		MirrorRegistryURL: m.MirrorRegistryURL.ValueString(),
		MirrorCA:          m.MirrorRegistryCA.ValueString(),
		HTTPProxy:         m.HTTPProxy.ValueString(),
		HTTPSProxy:        m.HTTPSProxy.ValueString(),
		NoProxy:           m.NoProxy.ValueString(),
	}
	var buf bytes.Buffer
	if err := deployScriptTmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ── health check ──────────────────────────────────────────────────────────────

func waitForService(ctx context.Context, baseURL string, timeout time.Duration) error {
	healthURL := strings.TrimRight(baseURL, "/") + "/api/assisted-install/v2/clusters"
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for assisted-service at %s", healthURL)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
}
