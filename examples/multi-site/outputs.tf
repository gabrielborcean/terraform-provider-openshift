output "cluster_id" {
  value       = openshift_cluster.this.cluster_id
  description = "Assisted Installer cluster ID."
}

output "status" {
  value       = openshift_cluster.this.status
  description = "Current cluster status."
}

output "discovery_iso_url" {
  value       = openshift_cluster.this.discovery_iso_url
  description = "Discovery ISO URL — boot servers from this (PXE/BMC handles this automatically)."
}

output "console_url" {
  value       = openshift_cluster.this.console_url
  description = "Cluster console URL (available after install completes)."
}

output "api_url" {
  value       = openshift_cluster.this.api_url
  description = "Cluster API URL."
}

output "kubeadmin_password" {
  value       = openshift_cluster.this.kubeadmin_password
  sensitive   = true
  description = "kubeadmin password."
}

output "boot_method" {
  value       = var.boot_method
  description = "Boot method used for this workspace."
}

output "agent_iso_path" {
  value       = var.boot_method == "agent" ? openshift_agent_iso.this[0].iso_path : null
  description = "Path to generated agent ISO (boot_method = agent only)."
}

output "agent_iso_checksum" {
  value       = var.boot_method == "agent" ? openshift_agent_iso.this[0].checksum_sha256 : null
  description = "SHA256 of agent ISO — verify after writing to USB: sha256sum /dev/sdX"
}

output "pxe_server_url" {
  value       = var.boot_method == "pxe" ? openshift_pxe_server.this[0].pxe_server_url : null
  description = "PXE HTTP server URL (boot_method = pxe only)."
}
