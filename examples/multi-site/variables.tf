# ── Boot method ───────────────────────────────────────────────────────────────
variable "boot_method" {
  description = "How to boot bare-metal nodes: pxe, bmc, or agent. Set per workspace in workspaces/<site>.tfvars."
  type        = string
  validation {
    condition     = contains(["pxe", "bmc", "agent"], var.boot_method)
    error_message = "boot_method must be one of: pxe, bmc, agent."
  }
}

# ── Cluster ───────────────────────────────────────────────────────────────────
variable "cluster_name" {
  description = "Name of the OpenShift cluster."
  type        = string
}

variable "openshift_version" {
  description = "OpenShift version, e.g. 4.14."
  type        = string
  default     = "4.14"
}

variable "base_dns_domain" {
  description = "Base DNS domain for the cluster."
  type        = string
}

variable "api_vip" {
  description = "Virtual IP for the cluster API."
  type        = string
}

variable "ingress_vip" {
  description = "Virtual IP for the cluster ingress."
  type        = string
}

variable "machine_network_cidr" {
  description = "CIDR for the machine network."
  type        = string
}

variable "pull_secret" {
  description = "Red Hat pull secret JSON."
  type        = string
  sensitive   = true
}

variable "ssh_public_key" {
  description = "SSH public key for cluster nodes."
  type        = string
}

variable "auto_install" {
  description = "Trigger installation automatically once hosts are ready."
  type        = bool
  default     = true
}

# ── Assisted Installer ────────────────────────────────────────────────────────
variable "assisted_service_url" {
  description = "Assisted Installer URL. Defaults to Red Hat hosted service."
  type        = string
  default     = "https://api.openshift.com"
}

variable "deploy_assisted_service" {
  description = "Deploy self-hosted Assisted Installer on the bastion. Set true for airgapped sites."
  type        = bool
  default     = false
}

variable "assisted_service_base_url" {
  description = "URL the self-hosted Assisted Installer will be reachable at. Used when deploy_assisted_service = true."
  type        = string
  default     = ""
}

variable "mirror_registry_url" {
  description = "Mirror registry URL for self-hosted Assisted Installer (airgapped). E.g. bastion.example.internal:8443"
  type        = string
  default     = ""
}

variable "mirror_registry_ca" {
  description = "PEM CA certificate for the mirror registry."
  type        = string
  default     = ""
}

variable "offline_token" {
  description = "Red Hat offline token for api.openshift.com. Not needed for self-hosted."
  type        = string
  sensitive   = true
  default     = ""
}

# ── Disconnected / airgapped ──────────────────────────────────────────────────
variable "additional_trust_bundle" {
  description = "PEM CA bundle for mirror registry (airgapped only)."
  type        = string
  default     = ""
}

variable "image_content_sources" {
  description = "Mirror registry mappings for disconnected installs."
  type = list(object({
    source  = string
    mirrors = list(string)
  }))
  default = []
}

# ── Bastion (shared by PXE and self-hosted Assisted Service) ──────────────────
variable "bastion_host" {
  description = "Hostname or IP of the bastion host."
  type        = string
  default     = ""
}

variable "bastion_user" {
  description = "SSH user on the bastion host."
  type        = string
  default     = "core"
}

variable "bastion_ssh_key" {
  description = "PEM-encoded SSH private key for the bastion host."
  type        = string
  sensitive   = true
  default     = ""
}

# ── PXE boot ──────────────────────────────────────────────────────────────────
variable "pxe_interface" {
  description = "Network interface on the bastion for PXE (provisioning network)."
  type        = string
  default     = "eth0"
}

variable "pxe_dhcp_range_start" {
  description = "Start of DHCP range for PXE clients. Leave empty for proxy DHCP."
  type        = string
  default     = ""
}

variable "pxe_dhcp_range_end" {
  description = "End of DHCP range for PXE clients."
  type        = string
  default     = ""
}

# ── BMC boot ──────────────────────────────────────────────────────────────────
variable "bmc_hosts" {
  description = "List of BMC hosts to boot via Redfish. Used when boot_method = bmc."
  type = list(object({
    name         = string
    bmc_address  = string
    bmc_username = string
    bmc_password = string
    vendor       = optional(string, "auto")
  }))
  default = []
}

# ── Agent ISO ─────────────────────────────────────────────────────────────────
variable "install_config" {
  description = "Contents of install-config.yaml. Used when boot_method = agent."
  type        = string
  default     = ""
}

variable "agent_config" {
  description = "Contents of agent-config.yaml. Used when boot_method = agent."
  type        = string
  default     = ""
}
