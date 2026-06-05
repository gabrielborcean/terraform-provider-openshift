terraform {
  required_providers {
    openshift = {
      source  = "gabrielborcean/openshift"
      version = ">= 0.4.8"
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

variable "offline_token"  { sensitive = true }
variable "pull_secret"    { sensitive = true }
variable "ssh_public_key" {}
# variable "aws_access_key_id"     { sensitive = true }
# variable "aws_secret_access_key" { sensitive = true }
