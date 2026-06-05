# Globex Corp — LACP bonded NICs, airgapped, agent ISO boot
boot_method = "agent"

cluster_name         = "ocp"
base_dns_domain      = "globex.example.internal"
machine_network_cidr = "10.2.0.0/24"

assisted_service_url = "http://bastion.globex.example.internal:8090"
offline_token        = ""   # self-hosted, no token needed

bastion_host = "bastion.globex.example.internal"
bastion_user = "core"

additional_trust_bundle = ""   # set to mirror registry CA
image_content_sources = [
  {
    source  = "quay.io/openshift-release-dev/ocp-release"
    mirrors = ["bastion.globex.example.internal:8443/openshift-release-dev/ocp-release"]
  },
  {
    source  = "quay.io/openshift-release-dev/ocp-v4.0-art-dev"
    mirrors = ["bastion.globex.example.internal:8443/openshift-release-dev/ocp-v4.0-art-dev"]
  },
]
