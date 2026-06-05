terraform {
  required_version = ">= 1.5.0"

  required_providers {
    openshift = {
      source  = "registry.terraform.io/r2dts/openshift"
      version = "~> 0.1"
    }
  }
}

provider "openshift" {
  install_binary  = "/usr/local/bin/openshift-install"
  oc_binary       = "/usr/local/bin/oc"
  pull_secret_file = "/root/.pull-secret.json"
}

# ──────────────────────────────────────────────
# 1. Mirror registry — Quay-based, self-hosted
# ──────────────────────────────────────────────

resource "openshift_mirror_registry" "internal" {
  hostname      = var.registry_hostname
  port          = var.registry_port
  storage_dir   = var.registry_storage_dir
  type          = "quay"
  quay_root     = "/opt/quay-install"
  init_user     = "admin"
  init_password = var.registry_password
}

# ──────────────────────────────────────────────
# 2. Mirror OCP release images + operators
# ──────────────────────────────────────────────

resource "openshift_image_mirror" "release" {
  registry_url    = openshift_mirror_registry.internal.registry_url
  pull_secret_file = var.pull_secret == "" ? "/root/.pull-secret.json" : "/dev/stdin"
  release_channel = var.ocp_release_channel

  operators = [
    {
      catalog = "registry.redhat.io/redhat/redhat-operator-index:v4.14"
      packages = [
        {
          name        = "local-storage-operator"
          channels    = ["stable"]
          min_version = ""
          max_version = ""
        },
        {
          name        = "openshift-gitops-operator"
          channels    = ["latest"]
          min_version = ""
          max_version = ""
        },
      ]
    }
  ]

  additional_images = [
    "registry.redhat.io/ubi8/ubi:latest",
    "registry.redhat.io/ubi9/ubi:latest",
  ]

  mirror_dir   = "/var/mirror-workspace"
  skip_missing = false
  dry_run      = false

  depends_on = [openshift_mirror_registry.internal]
}

# ──────────────────────────────────────────────
# 3. Generate install-config.yaml
# ──────────────────────────────────────────────

locals {
  # Build baremetal_hosts list with sensitive bmc_password from variable
  baremetal_hosts_config = [
    for h in var.baremetal_hosts : {
      name             = h.name
      bmc_address      = h.bmc_address
      bmc_username     = h.bmc_username
      bmc_password     = h.bmc_password
      boot_mac_address = h.boot_mac_address
      online           = true
      root_device_hints = {}
    }
  ]

  proxy_config_enabled = var.proxy_http != "" || var.proxy_https != ""
}

resource "openshift_install_config" "cluster" {
  cluster_name          = var.cluster_name
  base_domain           = var.base_domain
  api_vip               = var.api_vip
  ingress_vip           = var.ingress_vip
  control_plane_replicas = var.control_plane_replicas
  worker_replicas        = var.worker_replicas
  machine_network_cidr  = var.machine_network_cidr
  network_type          = "OVNKubernetes"
  cluster_network_cidr  = "10.128.0.0/14"
  service_network_cidr  = "172.30.0.0/16"
  pull_secret           = var.pull_secret
  ssh_key               = var.ssh_public_key
  output_dir            = var.install_dir

  # Use the CA cert from the mirror registry (self-signed)
  additional_trust_bundle = openshift_mirror_registry.internal.ca_cert != "" ? (
    openshift_mirror_registry.internal.ca_cert
  ) : var.additional_ca_bundle

  # Wire in the imageContentSources produced by oc mirror
  dynamic "image_content_sources" {
    for_each = openshift_image_mirror.release.image_content_sources
    content {
      source  = image_content_sources.value.source
      mirrors = image_content_sources.value.mirrors
    }
  }

  dynamic "proxy" {
    for_each = local.proxy_config_enabled ? [1] : []
    content {
      http_proxy  = var.proxy_http
      https_proxy = var.proxy_https
      no_proxy    = var.proxy_no_proxy
    }
  }

  dynamic "baremetal_hosts" {
    for_each = local.baremetal_hosts_config
    content {
      name             = baremetal_hosts.value.name
      bmc_address      = baremetal_hosts.value.bmc_address
      bmc_username     = baremetal_hosts.value.bmc_username
      bmc_password     = baremetal_hosts.value.bmc_password
      boot_mac_address = baremetal_hosts.value.boot_mac_address
      online           = baremetal_hosts.value.online
      root_device_hints = baremetal_hosts.value.root_device_hints
    }
  }

  depends_on = [openshift_image_mirror.release]
}

# ──────────────────────────────────────────────
# 4. Deploy the cluster
# ──────────────────────────────────────────────

resource "openshift_cluster" "this" {
  install_dir      = openshift_install_config.cluster.output_dir
  log_level        = "info"
  timeout          = "120m"
  destroy_on_delete = true

  depends_on = [openshift_install_config.cluster]
}

# ──────────────────────────────────────────────
# 5. Add a disconnected CatalogSource
# ──────────────────────────────────────────────

resource "openshift_catalog_source" "redhat_operators_mirror" {
  name         = "redhat-operators-mirror"
  namespace    = "openshift-marketplace"
  display_name = "Red Hat Operators (Mirrored)"
  image        = "${openshift_mirror_registry.internal.registry_url}/redhat/redhat-operator-index:v4.14"
  publisher    = "Red Hat"
  update_strategy = "RegistryPoll"
  poll_interval   = "30m"
  kubeconfig   = openshift_cluster.this.kubeconfig_path

  depends_on = [openshift_cluster.this]
}

# ──────────────────────────────────────────────
# 6. Subscribe to Local Storage Operator
# ──────────────────────────────────────────────

resource "openshift_subscription" "local_storage" {
  name                  = "local-storage-operator"
  namespace             = "openshift-local-storage"
  source                = openshift_catalog_source.redhat_operators_mirror.name
  source_namespace      = "openshift-marketplace"
  package_name          = "local-storage-operator"
  channel               = "stable"
  install_plan_approval = "Automatic"
  kubeconfig            = openshift_cluster.this.kubeconfig_path

  depends_on = [openshift_catalog_source.redhat_operators_mirror]
}

# ──────────────────────────────────────────────
# 7. Custom kernel arguments via MachineConfig
# ──────────────────────────────────────────────

resource "openshift_machine_config" "worker_hugepages" {
  name = "99-worker-hugepages"
  labels = {
    "machineconfiguration.openshift.io/role" = "worker"
  }

  kernel_arguments = [
    "hugepagesz=1G",
    "hugepages=16",
    "default_hugepagesz=1G",
  ]

  kubeconfig = openshift_cluster.this.kubeconfig_path
  depends_on = [openshift_cluster.this]
}

# ──────────────────────────────────────────────
# 8. Infra MachineSet for workload isolation
# ──────────────────────────────────────────────

resource "openshift_machine_set" "infra" {
  name       = "${var.cluster_name}-infra"
  namespace  = "openshift-machine-api"
  replicas   = 2
  cluster_id = openshift_cluster.this.cluster_id
  role       = "infra"
  kubeconfig = openshift_cluster.this.kubeconfig_path

  provider_spec = jsonencode({
    apiVersion = "baremetal.cluster.k8s.io/v1alpha1"
    kind       = "BareMetalMachineProviderSpec"
    image = {
      url      = "http://172.22.0.1/images/rhcos-4.14-x86_64.iso"
      checksum = ""
    }
    userData = {
      name = "worker-user-data"
    }
  })

  depends_on = [openshift_cluster.this]
}
