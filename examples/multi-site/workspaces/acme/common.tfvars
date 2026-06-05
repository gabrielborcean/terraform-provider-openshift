# ACME Corp — site config shared across all environments
# Secrets (pull_secret, ssh_public_key, offline_token) come from terraform.tfvars

boot_method = "pxe"

cluster_name         = "ocp"
base_dns_domain      = "acme.example.internal"
machine_network_cidr = "10.1.0.0/24"

bastion_host  = "bastion.acme.example.internal"
bastion_user  = "core"
# bastion_ssh_key from terraform.tfvars

pxe_interface        = "eth0"
pxe_dhcp_range_start = "10.1.0.100"
pxe_dhcp_range_end   = "10.1.0.200"
