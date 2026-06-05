terraform {
  required_providers {
    openshift = {
      source  = "gabrielborcean/openshift"
      version = ">= 0.4.15"
    }
  }
}

provider "openshift" {
  # When deploy_assisted_service = true the self-hosted service takes over;
  # use its URL instead of the Red Hat hosted one.
  assisted_service_url   = var.deploy_assisted_service ? var.assisted_service_base_url : var.assisted_service_url
  assisted_offline_token = var.offline_token != "" ? var.offline_token : null
}

# ── Self-hosted Assisted Installer (airgapped / self-managed) ─────────────────
# Set deploy_assisted_service = true in your customer's common.tfvars to run
# Assisted Installer on your own bastion instead of api.openshift.com.

resource "openshift_assisted_service" "bastion" {
  count = var.deploy_assisted_service ? 1 : 0

  bastion_host     = var.bastion_host
  bastion_user     = var.bastion_user
  bastion_ssh_key  = var.bastion_ssh_key != "" ? var.bastion_ssh_key : null
  service_base_url = var.assisted_service_base_url

  mirror_registry_url = var.mirror_registry_url != "" ? var.mirror_registry_url : null
  mirror_registry_ca  = var.mirror_registry_ca != "" ? var.mirror_registry_ca : null
}

# ── Cluster ───────────────────────────────────────────────────────────────────

resource "openshift_cluster" "this" {
  cluster_name         = "${var.cluster_name}-${terraform.workspace}"
  openshift_version    = var.openshift_version
  base_dns_domain      = var.base_dns_domain
  api_vip              = var.api_vip
  ingress_vip          = var.ingress_vip
  machine_network_cidr = var.machine_network_cidr
  pull_secret          = var.pull_secret
  ssh_public_key       = var.ssh_public_key
  create_infra_env     = true
  auto_install         = var.auto_install

  additional_trust_bundle = var.additional_trust_bundle != "" ? var.additional_trust_bundle : null
  image_content_sources   = length(var.image_content_sources) > 0 ? var.image_content_sources : null

  depends_on = [openshift_assisted_service.bastion]
}

# ── Boot: PXE ─────────────────────────────────────────────────────────────────
# Best for: unbonded NICs on a provisioning network, no BMC license needed.

resource "openshift_pxe_server" "this" {
  count = var.boot_method == "pxe" ? 1 : 0

  bastion_host    = var.bastion_host
  bastion_user    = var.bastion_user
  bastion_ssh_key = var.bastion_ssh_key

  iso_url         = openshift_cluster.this.discovery_iso_url
  interface       = var.pxe_interface
  dhcp_range_start = var.pxe_dhcp_range_start != "" ? var.pxe_dhcp_range_start : null
  dhcp_range_end   = var.pxe_dhcp_range_end != "" ? var.pxe_dhcp_range_end : null

  depends_on = [openshift_cluster.this]
}

# ── Boot: BMC / Redfish ───────────────────────────────────────────────────────
# Best for: any NIC config, requires paid BMC license on some hardware.

resource "openshift_bmc_boot" "this" {
  count = var.boot_method == "bmc" ? 1 : 0

  iso_url = openshift_cluster.this.discovery_iso_url
  hosts   = var.bmc_hosts

  depends_on = [openshift_cluster.this]
}

# ── Boot: Agent ISO ───────────────────────────────────────────────────────────
# Best for: no Redfish, LACP-bonded NICs, or fully airgapped.
# One ISO covers all servers. Boot each server manually in sequence.

resource "openshift_agent_iso" "this" {
  count = var.boot_method == "agent" ? 1 : 0

  install_config = var.install_config
  agent_config   = var.agent_config
  work_dir       = "/install-dir/${terraform.workspace}"

  # Optional: upload ISO to bastion for easy download/PXE serving
  bastion_host    = var.bastion_host != "" ? var.bastion_host : null
  bastion_user    = var.bastion_user
  bastion_ssh_key = var.bastion_ssh_key != "" ? var.bastion_ssh_key : null
}
