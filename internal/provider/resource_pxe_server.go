package provider

import (
	"context"
	"bytes"
	"fmt"
	"text/template"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &PXEServerResource{}

func NewPXEServerResource() resource.Resource { return &PXEServerResource{} }

type PXEServerResource struct{}

func (r *PXEServerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pxe_server"
}

// ── model ─────────────────────────────────────────────────────────────────────

type PXEServerModel struct {
	// SSH connection to bastion
	BastionHost   types.String `tfsdk:"bastion_host"`
	BastionPort   types.Int64  `tfsdk:"bastion_port"`
	BastionUser   types.String `tfsdk:"bastion_user"`
	BastionSSHKey types.String `tfsdk:"bastion_ssh_key"`

	// ISO source — one of iso_url (Assisted Installer) or iso_path (Agent-Based)
	ISOURL  types.String `tfsdk:"iso_url"`
	ISOPath types.String `tfsdk:"iso_path"`

	// Network config
	Interface    types.String `tfsdk:"interface"`
	DHCPRangeStart types.String `tfsdk:"dhcp_range_start"`
	DHCPRangeEnd   types.String `tfsdk:"dhcp_range_end"`
	HTTPPort     types.Int64  `tfsdk:"http_port"`

	// Computed
	PXEServerURL types.String `tfsdk:"pxe_server_url"`
}

// ── schema ────────────────────────────────────────────────────────────────────

func (r *PXEServerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Deploys a PXE server on a bastion host via SSH. " +
			"Installs dnsmasq (DHCP proxy + TFTP) and nginx (HTTP), extracts the kernel and initrd " +
			"from the discovery ISO, and serves them to nodes on the provisioning network. " +
			"Nodes boot, register with the Assisted Installer, and install automatically. " +
			"No BMC license required. Requires unbonded NICs during boot.",
		Attributes: map[string]schema.Attribute{
			"bastion_host": schema.StringAttribute{
				Required:    true,
				Description: "Hostname or IP of the bastion host.",
			},
			"bastion_port": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:     int64default.StaticInt64(22),
				Description: "SSH port on the bastion host.",
			},
			"bastion_user": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("core"),
				Description: "SSH user on the bastion host.",
			},
			"bastion_ssh_key": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "PEM-encoded SSH private key for the bastion host.",
			},
			"iso_url": schema.StringAttribute{
				Optional:    true,
				Description: "URL of the discovery ISO (use openshift_cluster.discovery_iso_url for Assisted Installer).",
			},
			"iso_path": schema.StringAttribute{
				Optional:    true,
				Description: "Path to a locally-built ISO on the bastion (use openshift_agent_iso.iso_path for Agent-Based).",
			},
			"interface": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:     stringdefault.StaticString("eth0"),
				Description: "Network interface on the bastion to serve PXE on (provisioning network).",
			},
			"dhcp_range_start": schema.StringAttribute{
				Optional:    true,
				Description: "Start of DHCP range for PXE clients, e.g. 10.0.1.100. Leave empty to use proxy DHCP (no range allocation).",
			},
			"dhcp_range_end": schema.StringAttribute{
				Optional:    true,
				Description: "End of DHCP range for PXE clients, e.g. 10.0.1.200.",
			},
			"http_port": schema.Int64Attribute{
				Optional: true, Computed: true,
				Default:     int64default.StaticInt64(8080),
				Description: "Port for the nginx HTTP server serving the ISO/kernel/initrd.",
			},
			"pxe_server_url": schema.StringAttribute{
				Computed:    true,
				Description: "Base URL of the PXE HTTP server, e.g. http://bastion.example.internal:8080.",
			},
		},
	}
}

// ── CRUD ──────────────────────────────────────────────────────────────────────

func (r *PXEServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PXEServerModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.ISOURL.IsNull() && plan.ISOPath.IsNull() {
		resp.Diagnostics.AddError("Configuration error", "One of iso_url or iso_path must be set.")
		return
	}

	client, err := sshConnect(&AssistedServiceModel{
		BastionHost:   plan.BastionHost,
		BastionPort:   plan.BastionPort,
		BastionUser:   plan.BastionUser,
		BastionSSHKey: plan.BastionSSHKey,
	})
	if err != nil {
		resp.Diagnostics.AddError("SSH connect", err.Error())
		return
	}
	defer client.Close()

	script, err := renderPXEScript(&plan)
	if err != nil {
		resp.Diagnostics.AddError("Rendering PXE script", err.Error())
		return
	}

	if out, err := sshRun(client, script); err != nil {
		resp.Diagnostics.AddError("Deploying PXE server", fmt.Sprintf("%s\n%s", err.Error(), out))
		return
	}

	plan.PXEServerURL = types.StringValue(fmt.Sprintf("http://%s:%d", plan.BastionHost.ValueString(), plan.HTTPPort.ValueInt64()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PXEServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PXEServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PXEServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	r.Create(ctx, resource.CreateRequest{Config: req.Config, Plan: req.Plan, ProviderMeta: req.ProviderMeta}, (*resource.CreateResponse)(resp))
}

func (r *PXEServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PXEServerModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := sshConnect(&AssistedServiceModel{
		BastionHost:   state.BastionHost,
		BastionPort:   state.BastionPort,
		BastionUser:   state.BastionUser,
		BastionSSHKey: state.BastionSSHKey,
	})
	if err != nil {
		return
	}
	defer client.Close()

	sshRun(client, `
		systemctl stop dnsmasq nginx 2>/dev/null || true
		rm -rf /opt/pxe-server
	`) //nolint:errcheck
}

// ── deploy script ─────────────────────────────────────────────────────────────

var pxeScriptTmpl = template.Must(template.New("pxe").Parse(`#!/usr/bin/env bash
set -euo pipefail

PXE_DIR=/opt/pxe-server
TFTP_DIR=${PXE_DIR}/tftp
HTTP_DIR=${PXE_DIR}/http
ISO_DIR=${PXE_DIR}/iso
HTTP_PORT={{.HTTPPort}}
IFACE={{.Interface}}

# ── install deps ──────────────────────────────────────────────────────────────
if command -v apt-get &>/dev/null; then
  apt-get install -y --no-install-recommends dnsmasq nginx isoinfo genisoimage xorriso
elif command -v dnf &>/dev/null; then
  dnf install -y dnsmasq nginx genisoimage xorriso
fi

mkdir -p "${TFTP_DIR}/pxelinux.cfg" "${HTTP_DIR}" "${ISO_DIR}"

# ── get the ISO ───────────────────────────────────────────────────────────────
{{- if .ISOURL}}
echo "Downloading discovery ISO..."
curl -fsSL "{{.ISOURL}}" -o "${ISO_DIR}/discovery.iso"
{{- else}}
echo "Using local ISO at {{.ISOPath}}"
cp "{{.ISOPath}}" "${ISO_DIR}/discovery.iso"
{{- end}}

# ── extract kernel + initrd from ISO ─────────────────────────────────────────
echo "Extracting kernel and initrd..."
ISO="${ISO_DIR}/discovery.iso"

# Try xorriso first (works for both BIOS and UEFI ISOs)
xorriso -osirrox on -indev "${ISO}" -extract /images/pxeboot/vmlinuz "${HTTP_DIR}/vmlinuz" 2>/dev/null || \
xorriso -osirrox on -indev "${ISO}" -extract /isolinux/vmlinuz "${HTTP_DIR}/vmlinuz"

xorriso -osirrox on -indev "${ISO}" -extract /images/pxeboot/initrd.img "${HTTP_DIR}/initrd.img" 2>/dev/null || \
xorriso -osirrox on -indev "${ISO}" -extract /isolinux/initrd.img "${HTTP_DIR}/initrd.img"

# Also serve the full ISO over HTTP for nodes that support HTTP boot
cp "${ISO}" "${HTTP_DIR}/discovery.iso"

# ── syslinux / GRUB for UEFI ─────────────────────────────────────────────────
# Extract GRUB EFI from ISO
xorriso -osirrox on -indev "${ISO}" -extract /EFI "${TFTP_DIR}/EFI" 2>/dev/null || true

# BIOS pxelinux
if command -v apt-get &>/dev/null; then
  cp /usr/lib/PXELINUX/pxelinux.0 "${TFTP_DIR}/" 2>/dev/null || true
  cp /usr/lib/syslinux/modules/bios/ldlinux.c32 "${TFTP_DIR}/" 2>/dev/null || true
fi

BASTION_IP=$(ip -4 addr show "${IFACE}" | grep -oP '(?<=inet\s)\d+\.\d+\.\d+\.\d+' | head -1)

cat > "${TFTP_DIR}/pxelinux.cfg/default" <<PXEOF
DEFAULT discovery
LABEL discovery
  KERNEL http://${BASTION_IP}:${HTTP_PORT}/vmlinuz
  INITRD http://${BASTION_IP}:${HTTP_PORT}/initrd.img
  APPEND coreos.live.rootfs_url=http://${BASTION_IP}:${HTTP_PORT}/discovery.iso
PXEOF

# ── nginx ─────────────────────────────────────────────────────────────────────
cat > /etc/nginx/conf.d/pxe.conf <<NGINXEOF
server {
    listen ${HTTP_PORT};
    root ${HTTP_DIR};
    autoindex on;
}
NGINXEOF

systemctl enable --now nginx

# ── dnsmasq ───────────────────────────────────────────────────────────────────
cat > /etc/dnsmasq.d/pxe.conf <<DNSEOF
interface=${IFACE}
bind-interfaces
dhcp-boot=pxelinux.0
enable-tftp
tftp-root=${TFTP_DIR}
{{- if and .DHCPRangeStart .DHCPRangeEnd}}
dhcp-range={{.DHCPRangeStart}},{{.DHCPRangeEnd}},12h
{{- else}}
# Proxy DHCP — works alongside existing DHCP server, no range allocation
dhcp-range=${BASTION_IP},proxy
{{- end}}
# UEFI support
dhcp-match=set:efi-x86_64,option:client-arch,7
dhcp-boot=tag:efi-x86_64,EFI/BOOT/BOOTX64.EFI
DNSEOF

systemctl enable --now dnsmasq

echo ""
echo "PXE server ready on ${BASTION_IP}:${HTTP_PORT}"
echo "  kernel:  http://${BASTION_IP}:${HTTP_PORT}/vmlinuz"
echo "  initrd:  http://${BASTION_IP}:${HTTP_PORT}/initrd.img"
echo "  iso:     http://${BASTION_IP}:${HTTP_PORT}/discovery.iso"
echo ""
echo "Nodes on the provisioning network will PXE boot automatically."
`))

type pxeScriptVars struct {
	ISOURL         string
	ISOPath        string
	Interface      string
	DHCPRangeStart string
	DHCPRangeEnd   string
	HTTPPort       int64
}

func renderPXEScript(m *PXEServerModel) (string, error) {
	vars := pxeScriptVars{
		ISOURL:         m.ISOURL.ValueString(),
		ISOPath:        m.ISOPath.ValueString(),
		Interface:      m.Interface.ValueString(),
		DHCPRangeStart: m.DHCPRangeStart.ValueString(),
		DHCPRangeEnd:   m.DHCPRangeEnd.ValueString(),
		HTTPPort:       m.HTTPPort.ValueInt64(),
	}
	var buf bytes.Buffer
	if err := pxeScriptTmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}

