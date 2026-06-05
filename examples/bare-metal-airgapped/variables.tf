variable "cluster_name" {
  description = "Name of the OpenShift cluster."
  type        = string
  default     = "ocp-baremetal"
}

variable "base_domain" {
  description = "Base DNS domain for the cluster (e.g. example.com)."
  type        = string
}

variable "api_vip" {
  description = "Virtual IP address for the cluster API."
  type        = string
}

variable "ingress_vip" {
  description = "Virtual IP address for cluster ingress."
  type        = string
}

variable "machine_network_cidr" {
  description = "CIDR of the bare metal host network."
  type        = string
  default     = "192.168.10.0/24"
}

variable "pull_secret" {
  description = "Red Hat pull secret JSON string."
  type        = string
  sensitive   = true
}

variable "ssh_public_key" {
  description = "SSH public key to inject into cluster nodes."
  type        = string
}

variable "registry_hostname" {
  description = "FQDN of the internal mirror registry host."
  type        = string
}

variable "registry_port" {
  description = "Port of the internal mirror registry."
  type        = number
  default     = 5000
}

variable "registry_password" {
  description = "Initial admin password for the mirror registry."
  type        = string
  sensitive   = true
}

variable "registry_storage_dir" {
  description = "Directory on the registry host to store image data."
  type        = string
  default     = "/data/registry"
}

variable "ocp_release_channel" {
  description = "OpenShift release channel to mirror (e.g. stable-4.14)."
  type        = string
  default     = "stable-4.14"
}

variable "install_dir" {
  description = "Local directory for install-config.yaml and cluster assets."
  type        = string
  default     = "/tmp/ocp-install"
}

variable "control_plane_replicas" {
  description = "Number of control plane nodes."
  type        = number
  default     = 3
}

variable "worker_replicas" {
  description = "Number of initial worker nodes."
  type        = number
  default     = 3
}

variable "baremetal_hosts" {
  description = "List of bare metal hosts for IPI provisioning."
  type = list(object({
    name             = string
    bmc_address      = string
    bmc_username     = string
    bmc_password     = string
    boot_mac_address = string
  }))
  sensitive = false
}

variable "additional_ca_bundle" {
  description = "PEM-encoded CA bundle for the internal mirror registry TLS certificate."
  type        = string
  default     = ""
}

variable "proxy_http" {
  description = "HTTP proxy URL (leave empty if no proxy)."
  type        = string
  default     = ""
}

variable "proxy_https" {
  description = "HTTPS proxy URL (leave empty if no proxy)."
  type        = string
  default     = ""
}

variable "proxy_no_proxy" {
  description = "Comma-separated list of no-proxy hosts."
  type        = string
  default     = ""
}
