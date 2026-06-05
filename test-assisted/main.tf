terraform {
  required_providers {
    openshift = {
      source  = "gabrielborcean/openshift"
      version = ">= 0.4.3"
    }
  }
}

provider "openshift" {
  assisted_service_url   = "https://api.openshift.com/api/assisted-install/v2"
  assisted_offline_token = var.offline_token
}

resource "openshift_cluster" "test" {
  cluster_name         = "tf-test"
  openshift_version    = "4.14"
  base_dns_domain      = "example.com"
  api_vip              = "10.0.1.10"
  ingress_vip          = "10.0.1.11"
  machine_network_cidr = "10.0.1.0/24"
  pull_secret          = var.pull_secret
  ssh_public_key       = var.ssh_public_key

  auto_install     = false   # don't touch real hardware
  create_infra_env = true    # generate discovery ISO URL
}

output "cluster_id"    { value = openshift_cluster.test.cluster_id }
output "status"        { value = openshift_cluster.test.status }
output "discovery_iso" { value = openshift_cluster.test.discovery_iso_url }

variable "offline_token"  { sensitive = true }
variable "pull_secret"    { sensitive = true }
variable "ssh_public_key" {}
