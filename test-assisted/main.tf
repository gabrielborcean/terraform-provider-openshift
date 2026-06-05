terraform {
  required_providers {
    openshift = {
      source  = "gabrielborcean/openshift"
      version = ">= 0.4.14"
    }
  }
}

provider "openshift" {
  assisted_service_url   = "https://api.openshift.com"
  assisted_offline_token = var.offline_token
}

# ── Self-hosted Assisted Installer on bastion (optional) ──────────────────────
# Uncomment to deploy Assisted Service on your own bastion instead of api.openshift.com
#
# resource "openshift_assisted_service" "bastion" {
#   bastion_host     = "bastion.example.internal"
#   bastion_user     = "core"
#   bastion_ssh_key  = file("/secrets/ssh/id_rsa")
#   service_base_url = "http://bastion.example.internal:8090"
# }

# ── Agent ISO — bake install-config + agent-config into one ISO ──────────────
# Write to USB once, boot each server from it sequentially.
# One ISO works for all servers — MACs and roles are resolved at boot time.
#
# resource "openshift_agent_iso" "iso" {
#   install_config = file("install-config.yaml")
#   agent_config   = file("agent-config.yaml")
#   work_dir       = "/install-dir/agent-iso"
#
#   # Optional: upload to bastion for PXE serving
#   # bastion_host    = "bastion.example.internal"
#   # bastion_user    = "core"
#   # bastion_ssh_key = file("/secrets/ssh/id_rsa")
# }
#
# output "iso_path"   { value = openshift_agent_iso.iso.iso_path }
# output "checksum"   { value = openshift_agent_iso.iso.checksum_sha256 }
# output "iso_size"   { value = openshift_agent_iso.iso.iso_size_bytes }
#
# ── PXE server — zero-touch boot for unbonded NICs, no BMC license needed ────
# Serves the discovery ISO kernel/initrd over TFTP + HTTP.
# Does NOT work with LACP-bonded NICs (bond isn't up during early boot).
#
# resource "openshift_pxe_server" "pxe" {
#   bastion_host    = "bastion.example.internal"
#   bastion_user    = "core"
#   bastion_ssh_key = file("/secrets/ssh/id_rsa")
#
#   iso_url   = openshift_cluster.test.discovery_iso_url   # Assisted Installer
#   # iso_path = openshift_agent_iso.iso.bastion_iso_url   # Agent-Based
#
#   interface       = "eth0"          # provisioning network interface
#   dhcp_range_start = "10.0.1.100"  # omit to use proxy DHCP alongside existing DHCP
#   dhcp_range_end   = "10.0.1.200"
# }

# ── BMC boot — mount discovery ISO via Redfish, zero-touch ───────────────────
# Uncomment after openshift_cluster is created to boot servers automatically
#
# resource "openshift_bmc_boot" "nodes" {
#   iso_url = openshift_cluster.test.discovery_iso_url
#
#   hosts = [
#     {
#       name         = "master-0"
#       bmc_address  = "https://10.0.0.10"
#       bmc_username = "admin"
#       bmc_password = var.bmc_password
#       vendor       = "auto"
#     },
#   ]
# }
#
# variable "bmc_password" { sensitive = true }

# ── Bare-metal cluster via Assisted Installer ─────────────────────────────────
resource "openshift_cluster" "test" {
  cluster_name         = "tf-test"
  openshift_version    = "4.14"
  base_dns_domain      = "example.com"
  api_vip              = "10.0.1.10"
  ingress_vip          = "10.0.1.11"
  machine_network_cidr = "10.0.1.0/24"
  pull_secret          = var.pull_secret
  ssh_public_key       = var.ssh_public_key

  auto_install     = false  # don't touch real hardware
  create_infra_env = true   # generate discovery ISO URL
}

output "cluster_id"    { value = openshift_cluster.test.cluster_id }
output "status"        { value = openshift_cluster.test.status }
output "discovery_iso" { value = openshift_cluster.test.discovery_iso_url }

# ── AWS cluster via openshift-install IPI ─────────────────────────────────────
# Uncomment to create a cluster on AWS instead of bare metal
#
# resource "openshift_cluster_aws" "prod" {
#   cluster_name          = "prod-ocp"
#   base_domain           = "example.com"
#   region                = "us-east-1"
#   pull_secret           = var.pull_secret
#   ssh_public_key        = var.ssh_public_key
#   aws_access_key_id     = var.aws_access_key_id
#   aws_secret_access_key = var.aws_secret_access_key
#   install_dir           = "/install-dir"
# }
#
# output "console_url"  { value = openshift_cluster_aws.prod.console_url }
# output "kubeconfig"   { value = openshift_cluster_aws.prod.kubeconfig  sensitive = true }

# ── Variables — set values in terraform.tfvars (gitignored, auto-loaded) ──────
# Copy terraform.tfvars.example → terraform.tfvars and fill in your values.
variable "offline_token"  { sensitive = true }
variable "pull_secret"    { sensitive = true }
variable "ssh_public_key" {}
# variable "bmc_password"          { sensitive = true }
# variable "aws_access_key_id"     { sensitive = true }
# variable "aws_secret_access_key" { sensitive = true }
