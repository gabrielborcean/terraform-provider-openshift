# terraform-provider-openshift

Terraform provider for OpenShift deployments — bare metal (airgapped or connected) and AWS.

---

## New here? Four steps to get running

```sh
# 1. Check prerequisites and see what secrets you need
make setup

# 2. Fill in your config — one file, that's it
cp test-assisted/terraform.tfvars.example test-assisted/terraform.tfvars
$EDITOR test-assisted/terraform.tfvars

# 3. Build the container image (do this once — takes ~5 min)
make image

# 4. Deploy
make run-registry WORKSPACE=$(pwd)/test-assisted
```

All credentials live in `terraform.tfvars` — Terraform loads it automatically, no `-var` flags ever.

**To iterate on provider code without publishing a release:**
```sh
make run-local WORKSPACE=$(pwd)/test-assisted   # builds from source, no registry needed
```

---

Manages the full lifecycle from a single `terraform apply`:

| Target | Approach |
|---|---|
| **Bare metal / on-prem** | Assisted Installer REST API — nodes boot a discovery ISO, phone home, cluster installs automatically |
| **AWS** | `openshift-install` IPI — fully automated, creates all AWS infrastructure (VPC, Route53, ELBs, EC2) |

---

## Resources

| Resource | What it manages |
|---|---|
| `openshift_agent_iso` | Generates an Agent-Based Installer ISO from your existing `install-config.yaml` and `agent-config.yaml`. One ISO covers all servers (MACs + roles baked in). Write to USB once, boot each server sequentially. Optionally uploads to bastion for PXE serving |
| `openshift_assisted_service` | Deploys the Assisted Installer service on a bastion host via SSH (podman). Use this for self-hosted/airgapped environments instead of api.openshift.com |
| `openshift_bmc_boot` | Mounts the discovery ISO via Redfish virtual media and reboots bare-metal hosts. Supports Dell iDRAC, HPE iLO, Supermicro, and generic Redfish v1. Zero-touch — no manual ISO flashing. Requires paid BMC license on some hardware |
| `openshift_cluster` | Bare-metal OpenShift cluster via the Assisted Installer API. Supports disconnected registries, proxy, and custom infra-env ISO |
| `openshift_cluster_aws` | OpenShift cluster on AWS via openshift-install IPI. Creates all AWS infrastructure automatically |
| `openshift_pxe_server` | Deploys a PXE server on the bastion (dnsmasq + nginx). Serves the discovery ISO kernel/initrd over TFTP/HTTP. Zero-touch for servers with unbonded NICs on the provisioning network. No BMC license required |
| `openshift_mirror_registry` | Sets up a Quay mirror registry on a bastion host |
| `openshift_image_mirror` | Runs `oc mirror` to sync OCP release images and operator catalogs to the mirror registry |
| `openshift_catalog_source` | `operators.coreos.com/v1alpha1 CatalogSource` on the cluster |
| `openshift_subscription` | OLM `Subscription`, tracks `currentCSV` / `installedCSV` |
| `openshift_machine_config` | `machineconfiguration.openshift.io/v1 MachineConfig` (files, systemd units, kernel args) |
| `openshift_machine_set` | `machine.openshift.io/v1beta1 MachineSet` |

---

## Deployment flows

### Bare metal — connected (api.openshift.com)

Nodes can reach the internet. Cluster management goes through Red Hat's hosted Assisted Service.

```hcl
provider "openshift" {
  assisted_service_url   = "https://api.openshift.com"
  assisted_offline_token = var.offline_token   # from console.redhat.com/openshift/token
}

resource "openshift_cluster" "prod" {
  cluster_name         = "prod-ocp"
  openshift_version    = "4.14"
  base_dns_domain      = "example.internal"
  api_vip              = "10.0.1.10"
  ingress_vip          = "10.0.1.11"
  machine_network_cidr = "10.0.1.0/24"
  pull_secret          = var.pull_secret
  ssh_public_key       = var.ssh_public_key
  create_infra_env     = true   # generates discovery_iso_url
  auto_install         = true   # installs once hosts are ready
}

output "discovery_iso" { value = openshift_cluster.prod.discovery_iso_url }
```

Boot your bare-metal servers from the discovery ISO URL. Once they register and pass validation, the install proceeds automatically.

---

### Bare metal — airgapped (self-hosted Assisted Installer)

No internet access. Assisted Installer runs on the bastion alongside the mirror registry.

```hcl
# 1. Mirror registry on the bastion
resource "openshift_mirror_registry" "bastion" {
  hostname    = "bastion.example.internal"
  ssh_user    = "core"
  ssh_private_key = var.ssh_private_key
}

# 2. Mirror OCP images to the registry
resource "openshift_image_mirror" "ocp" {
  registry_url    = openshift_mirror_registry.bastion.registry_url
  pull_secret     = var.pull_secret
  ocp_version     = "4.14"
  depends_on      = [openshift_mirror_registry.bastion]
}

# 3. Deploy Assisted Installer on the bastion
resource "openshift_assisted_service" "bastion" {
  bastion_host     = "bastion.example.internal"
  bastion_user     = "core"
  bastion_ssh_key  = var.ssh_private_key
  service_base_url = "http://bastion.example.internal:8090"

  mirror_registry_url = openshift_mirror_registry.bastion.registry_url
  mirror_registry_ca  = openshift_mirror_registry.bastion.ca_cert
  depends_on          = [openshift_image_mirror.ocp]
}

# 4. Create the cluster — points at the self-hosted service
resource "openshift_cluster" "prod" {
  assisted_service_url = openshift_assisted_service.bastion.api_url
  cluster_name         = "prod-ocp"
  openshift_version    = "4.14"
  base_dns_domain      = "example.internal"
  api_vip              = "10.0.1.10"
  ingress_vip          = "10.0.1.11"
  machine_network_cidr = "10.0.1.0/24"
  pull_secret          = var.pull_secret
  ssh_public_key       = var.ssh_public_key
  create_infra_env     = true
  auto_install         = true

  additional_trust_bundle = openshift_mirror_registry.bastion.ca_cert
  image_content_sources = [
    {
      source  = "quay.io/openshift-release-dev/ocp-release"
      mirrors = ["bastion.example.internal:8443/openshift-release-dev/ocp-release"]
    },
    {
      source  = "quay.io/openshift-release-dev/ocp-v4.0-art-dev"
      mirrors = ["bastion.example.internal:8443/openshift-release-dev/ocp-v4.0-art-dev"]
    }
  ]
}

# 5. Boot bare-metal servers from the discovery ISO via Redfish — no manual ISO flashing
resource "openshift_bmc_boot" "nodes" {
  iso_url = openshift_cluster.prod.discovery_iso_url

  hosts = [
    {
      name         = "master-0"
      bmc_address  = "https://10.0.0.10"
      bmc_username = "admin"
      bmc_password = var.bmc_password
      vendor       = "dell"   # dell, hpe, supermicro, or auto
    },
    {
      name         = "master-1"
      bmc_address  = "https://10.0.0.11"
      bmc_username = "admin"
      bmc_password = var.bmc_password
    },
    {
      name         = "worker-0"
      bmc_address  = "https://10.0.0.20"
      bmc_username = "admin"
      bmc_password = var.bmc_password
    },
  ]

  depends_on = [openshift_cluster.prod]
}
```

---

### AWS (openshift-install IPI)

Fully automated — creates VPC, subnets, security groups, Route53 records, load balancers, and EC2 instances. Requires a Route53 public hosted zone for `base_domain`.

```hcl
provider "openshift" {}

resource "openshift_cluster_aws" "prod" {
  cluster_name          = "prod-ocp"
  base_domain           = "example.com"        # must be a Route53 hosted zone
  region                = "us-east-1"
  pull_secret           = var.pull_secret
  ssh_public_key        = var.ssh_public_key
  aws_access_key_id     = var.aws_access_key_id
  aws_secret_access_key = var.aws_secret_access_key

  openshift_version           = "4.14"
  control_plane_instance_type = "m6i.xlarge"
  worker_instance_type        = "m6i.xlarge"
  worker_replicas             = 3
  install_dir                 = "/install-dir"  # must persist between apply and destroy
}

output "console_url"       { value = openshift_cluster_aws.prod.console_url }
output "api_url"           { value = openshift_cluster_aws.prod.api_url }
output "kubeadmin_password" {
  value     = openshift_cluster_aws.prod.kubeadmin_password
  sensitive = true
}
```

Blocks for ~40 minutes while the cluster installs. The `install_dir` must remain on disk — it contains `metadata.json` which is required for `terraform destroy` to tear down AWS resources.

---

### Post-install operator management (any cluster)

These resources work with any cluster via kubeconfig.

```hcl
provider "openshift" {
  kubeconfig = "/install-dir/auth/kubeconfig"
}

resource "openshift_catalog_source" "redhat" {
  name      = "redhat-operators"
  namespace = "openshift-marketplace"
  image     = "registry.redhat.io/redhat/redhat-operator-index:v4.14"
}

resource "openshift_subscription" "acm" {
  name             = "advanced-cluster-management"
  namespace        = "open-cluster-management"
  source           = openshift_catalog_source.redhat.name
  source_namespace = "openshift-marketplace"
  channel          = "release-2.10"
}
```

---

## Provider configuration

```hcl
provider "openshift" {
  # Assisted Installer (bare metal)
  assisted_service_url   = "https://api.openshift.com"   # or self-hosted URL
  assisted_offline_token = var.offline_token              # from console.redhat.com/openshift/token
  # assisted_service_token = var.token                   # static bearer token alternative

  # Post-install resources
  kubeconfig = "/install-dir/auth/kubeconfig"   # defaults to KUBECONFIG env var
  oc_binary  = "/usr/local/bin/oc"              # defaults to 'oc' on PATH
}
```

All fields are optional — set only what the resources you're using need.

---

## Prerequisites

Nothing needs to be installed on your workstation. All tooling runs inside a container image you build once.

Required files before you start:

| File | Where to get it |
|---|---|
| `secrets/pull-secret.json` | [console.redhat.com → Downloads → Pull Secret](https://console.redhat.com/openshift/downloads) |
| `secrets/id_rsa.pub` | Your SSH public key (`ssh-keygen` if you don't have one) |
| `secrets/offline-token.txt` | [console.redhat.com/openshift/token](https://console.redhat.com/openshift/token) |

---

## Config

All credentials go in one file — `terraform.tfvars` in your workspace. It's gitignored and auto-loaded by Terraform:

```sh
cp test-assisted/terraform.tfvars.example test-assisted/terraform.tfvars
$EDITOR test-assisted/terraform.tfvars
```

```hcl
# terraform.tfvars
offline_token  = "eyJ..."                    # console.redhat.com/openshift/token
pull_secret    = '{"auths": ...}'            # console.redhat.com/openshift/downloads
ssh_public_key = "ssh-rsa AAAA..."
```

Run `make setup` to check which secrets files are present in `./secrets/`.

---

## Multi-site deployments

One configuration, one workspace per site, independent state. Each site picks its own boot method.

```sh
# First time — copy and fill in shared secrets
cp examples/multi-site/terraform.tfvars.example examples/multi-site/terraform.tfvars
$EDITOR examples/multi-site/terraform.tfvars

# Deploy each site
make site-apply SITE=site-a   # PXE boot, unbonded NICs
make site-apply SITE=site-b   # BMC/Redfish boot
make site-apply SITE=site-c   # Agent ISO, airgapped

# Review before deploying
make site-plan SITE=site-a

# Tear down one site without touching others
make site-destroy SITE=site-b

# See all workspaces
make site-list
```

Per-site config lives in `examples/multi-site/workspaces/<site>.tfvars`. Shared secrets (pull secret, SSH key, offline token) live in `terraform.tfvars` and apply to all sites.

```
examples/multi-site/
├── main.tf                     # cluster + conditional boot resource
├── variables.tf                # all variables
├── outputs.tf
├── terraform.tfvars.example    # copy → terraform.tfvars, fill in secrets
└── workspaces/
    ├── site-a.tfvars           # PXE boot, unbonded NICs
    ├── site-b.tfvars           # BMC/Redfish boot
    └── site-c.tfvars           # Agent ISO, fully airgapped
```

---

## Make targets

```
make                    Show all targets (same as make help)

# First time
make setup              Check prerequisites, show which secrets are missing
make image              Build the ocp-toolbox container image (do once)

# Multi-site (recommended)
make site-apply SITE=site-a    Deploy a site (workspace per site)
make site-plan  SITE=site-a    Plan changes for a site
make site-destroy SITE=site-a  Tear down a site without touching others
make site-list                 List all site workspaces

# Single deploy
make plan               terraform plan — review changes before applying
make run-local          Build provider from source + terraform apply
make run-registry       Pull provider from registry.terraform.io + terraform apply
make destroy            terraform destroy — tear down cluster and all infrastructure
make shell              Interactive shell inside the container

# Validate
make validate           terraform validate — syntax and schema check only
make test-registry      Smoke-test: pull provider from registry, run init only

# Release
make publish            Build + push a signed GitHub release (set RELEASE_TAG=vX.Y.Z)

# Development
make build              Build provider binary locally (requires Go)
make install            Install provider to ~/.terraform.d/plugins/
make test               Run unit tests
make testacc            Run acceptance tests (requires live cluster)
make fmt                Format Go source
make lint               Run golangci-lint
make docs               Regenerate provider docs with tfplugindocs
make clean              Remove built binary
```

---

## Mount reference

| Container path | Default host path | Override variable |
|---|---|---|
| `/workspace` | `./examples/bare-metal-airgapped` | `WORKSPACE` |
| `/install-dir` | `./.install-dir` | `INSTALL_DIR` |
| `/secrets/pull-secret.json` | `./secrets/pull-secret.json` | `SECRETS_DIR` |
| `/secrets/ssh/id_rsa.pub` | `./secrets/id_rsa.pub` | `SECRETS_DIR` |
| `/secrets/offline-token.txt` | `./secrets/offline-token.txt` | `SECRETS_DIR` |

---

## Publishing to the Terraform Registry

### One-time setup

1. **Generate a GPG key** (must be RSA or DSA — Ed25519 is not supported by the registry):
   ```sh
   gpg --full-generate-key          # choose RSA 4096, no expiry
   gpg --armor --export KEY_ID > gpg-public-key.asc
   gpg --armor --export-secret-keys KEY_ID > gpg-private-key.asc
   ```

2. **Add secrets to GitHub** (Settings → Secrets → Actions):
   - `GPG_PRIVATE_KEY` — contents of `gpg-private-key.asc`
   - `PASSPHRASE` — your GPG passphrase

3. **Connect the registry**:
   - Sign in at [registry.terraform.io](https://registry.terraform.io) with GitHub
   - Publish → Provider → select `terraform-provider-openshift`
   - Paste `gpg-public-key.asc`

### Releasing

```sh
git tag -a v0.x.0 -m "v0.x.0"
git push origin main v0.x.0
```

GitHub Actions builds and signs binaries for Linux/macOS/Windows (amd64 + arm64) and publishes the release. The Terraform Registry picks it up within minutes.

---

## Build arguments

| Argument | Default | Description |
|---|---|---|
| `GO_VERSION` | `1.25` | Go toolchain version |
| `TERRAFORM_VERSION` | `1.8.5` | Terraform CLI version |
| `OCP_VERSION` | `4.14.37` | Controls `openshift-install`, `oc`, `oc-mirror` versions |
| `MIRROR_REGISTRY_VERSION` | `2.0.3` | Quay mirror-registry version |

---

## Directory layout

```
.
├── Dockerfile
├── entrypoint.sh
├── GNUmakefile
├── main.go
├── go.mod / go.sum
├── internal/provider/
│   ├── provider.go
│   ├── assisted_client.go          # Assisted Installer REST API client
│   ├── token.go                    # Red Hat SSO offline token auto-refresh
│   ├── compat.go                   # OCP version compatibility matrix
│   ├── exec.go                     # CLI invocation helpers
│   ├── kube.go                     # Kubernetes dynamic client helpers
│   ├── resource_assisted_service.go  # Self-hosted Assisted Installer on bastion
│   ├── resource_cluster.go           # Bare-metal cluster (Assisted Installer API)
│   ├── resource_cluster_aws.go       # AWS cluster (openshift-install IPI)
│   ├── resource_mirror_registry.go
│   ├── resource_image_mirror.go
│   ├── resource_catalog_source.go
│   ├── resource_subscription.go
│   ├── resource_machine_config.go
│   └── resource_machine_set.go
├── docs/                           # Auto-generated by tfplugindocs
├── scripts/
│   ├── podman-run.sh
│   └── publish.sh
├── secrets/                        # Gitignored — place pull-secret, SSH key, token here
└── test-assisted/
    └── main.tf                     # Live test config
```
