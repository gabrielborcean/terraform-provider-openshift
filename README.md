# terraform-provider-openshift

Terraform provider for airgapped, on-premises OpenShift deployments on bare metal.

Manages the full lifecycle: mirror registry setup, image mirroring, cluster install, and post-install operator/machine configuration — all from a single `terraform apply`.

---

## Resources

| Resource | What it manages |
|---|---|
| `openshift_install_config` | Generates `install-config.yaml` for bare-metal IPI (VIPs, BMC hosts, disconnected registries, CA bundle, proxy) |
| `openshift_cluster` | Drives `openshift-install create / destroy cluster` |
| `openshift_mirror_registry` | Sets up a Quay or plain container registry on a bastion host |
| `openshift_image_mirror` | Runs `oc mirror` to sync OCP release images and operator catalogs |
| `openshift_catalog_source` | `operators.coreos.com/v1alpha1 CatalogSource` on the cluster |
| `openshift_subscription` | OLM `Subscription`, tracks `currentCSV` / `installedCSV` |
| `openshift_machine_config` | `machineconfiguration.openshift.io/v1 MachineConfig` (files, systemd units, kernel args) |
| `openshift_machine_set` | `machine.openshift.io/v1beta1 MachineSet` |

---

## Prerequisites

Nothing needs to be installed on your workstation. All tooling runs inside a container image you build once.

Required files on the host before you start:

| File | Where to get it |
|---|---|
| `pull-secret.json` | [console.redhat.com → Downloads → Pull Secret](https://console.redhat.com/openshift/downloads) |
| `id_rsa.pub` | Your SSH public key (`ssh-keygen` if you don't have one) |

---

## Quickstart

### 1. Clone and build the image

```sh
git clone <this-repo>
cd terraform-provider-openshift

# Build with defaults (OCP 4.14.37, Terraform 1.8.5)
make image

# Or pin specific versions
make image OCP_VERSION=4.15.12 TF_VERSION=1.8.5
```

The image is tagged `ocp-toolbox:latest` locally. It contains:
- `terraform` + the provider binary (pre-installed, no registry needed)
- `openshift-install`
- `oc` + `kubectl`
- `oc-mirror`
- `mirror-registry` (Quay)
- `skopeo`
- `make`

### 2. Place secrets

```sh
mkdir -p secrets  # gitignored
cp ~/Downloads/pull-secret.json secrets/pull-secret.json
cp ~/.ssh/id_rsa.pub            secrets/id_rsa.pub
chmod 600 secrets/pull-secret.json
```

### 3. Configure your deployment

Copy the example and fill in your values:

```sh
cp -r examples/bare-metal-airgapped my-cluster
```

Edit `my-cluster/variables.tf` (or create a `my-cluster/terraform.tfvars`):

```hcl
cluster_name       = "prod-ocp"
base_domain        = "example.internal"
api_vip            = "10.0.1.10"
ingress_vip        = "10.0.1.11"
machine_network    = "10.0.1.0/24"
registry_hostname  = "bastion.example.internal"

baremetal_hosts = [
  {
    name             = "master-0"
    bmc_address      = "idrac-virtualmedia+https://10.0.0.10/redfish/v1/Systems/System.Embedded.1"
    bmc_username     = "admin"
    bmc_password     = "changeme"
    boot_mac_address = "aa:bb:cc:dd:ee:01"
  },
  # ... more hosts
]
```

### 4. Run

```sh
# Interactive shell inside the container (workspace = my-cluster/)
make run WORKSPACE=$(pwd)/my-cluster

# Or run terraform directly (non-interactive)
make run-terraform WORKSPACE=$(pwd)/my-cluster
```

Inside the shell, standard Terraform workflow applies:

```sh
terraform init
terraform plan
terraform apply
```

---

## Mount reference

All paths are configurable via `make` variables.

| Container path | Default host path | Override variable |
|---|---|---|
| `/workspace` | `./examples/bare-metal-airgapped` | `WORKSPACE` |
| `/install-dir` | `./.install-dir` | `INSTALL_DIR` |
| `/secrets/pull-secret.json` | `./secrets/pull-secret.json` | `SECRETS_DIR` |
| `/secrets/ssh/id_rsa.pub` | `./secrets/id_rsa.pub` | `SECRETS_DIR` |

The `:Z` SELinux relabeling flag is applied to all mounts — required on RHEL/Fedora/CentOS hosts. Remove it if running on macOS or a non-SELinux system.

---

## Publishing to the Terraform Registry

### One-time setup

1. **Generate a GPG key** and export it:
   ```sh
   gpg --full-generate-key          # RSA 4096, no expiry
   gpg --armor --export KEY_ID > gpg-public-key.asc
   gpg --armor --export-secret-keys KEY_ID > gpg-private-key.asc
   ```

2. **Add secrets to the GitHub repo** (Settings → Secrets → Actions):
   - `GPG_PRIVATE_KEY` — contents of `gpg-private-key.asc`
   - `PASSPHRASE` — your GPG key passphrase

3. **Connect the registry**:
   - Sign in at [registry.terraform.io](https://registry.terraform.io) with GitHub
   - Publish → Provider → select `terraform-provider-openshift`
   - Paste the contents of `gpg-public-key.asc`

### Releasing

```sh
git tag v0.1.0
git push origin v0.1.0
```

GitHub Actions builds binaries for Linux/macOS/Windows (amd64 + arm64), signs the checksums with your GPG key, and creates a GitHub Release. The Terraform Registry detects the new tag automatically within a few minutes.

The provider will then be available as:
```hcl
terraform {
  required_providers {
    openshift = {
      source  = "gabrielborcean/openshift"
      version = "~> 0.1"
    }
  }
}
```

---

## Make targets

```
make image              Build the container image with podman
make run                Interactive shell with mounts
make run-terraform      Run terraform init && apply non-interactively
make build              Build the provider binary locally (requires Go)
make install            Install provider to ~/.terraform.d/plugins/
make test               Run unit tests
make testacc            Run acceptance tests (requires live cluster)
make fmt                Format Go source
make lint               Run golangci-lint
make clean              Remove built binary
```

---

## Provider configuration

```hcl
provider "openshift" {
  kubeconfig       = "/workspace/kubeconfig"          # optional, auto-detected
  install_binary   = "/usr/local/bin/openshift-install"
  oc_binary        = "/usr/local/bin/oc"
  pull_secret_file = "/secrets/pull-secret.json"
  ssh_key          = file("/secrets/ssh/id_rsa.pub")
}
```

All fields are optional and fall back to environment variables or binary detection on `PATH`. Inside the container the binaries are already on `PATH` and secrets are available at `/secrets/`.

---

## Environment variables

| Variable | Purpose |
|---|---|
| `KUBECONFIG` | Path to kubeconfig (default: `/workspace/kubeconfig`) |
| `OPENSHIFT_PULL_SECRET_FILE` | Set automatically by entrypoint if `/secrets/pull-secret.json` is mounted |
| `OPENSHIFT_SSH_KEY` | Set automatically by entrypoint if `/secrets/ssh/id_rsa.pub` is mounted |
| `TF_CLI_ARGS_init` | Pre-set to `-plugin-dir=/usr/local/lib/tf-plugins` so `terraform init` uses the bundled provider |

---

## Build arguments

| Argument | Default | Description |
|---|---|---|
| `GO_VERSION` | `1.25` | Go toolchain version for the builder stage |
| `TERRAFORM_VERSION` | `1.8.5` | Terraform CLI version |
| `OCP_VERSION` | `4.14.37` | OCP release (controls `openshift-install`, `oc`, `oc-mirror` versions) |
| `MIRROR_REGISTRY_VERSION` | `2.0.3` | Quay mirror-registry version |

---

## Directory layout

```
.
├── Dockerfile                        # Multi-stage image (builder + runtime)
├── entrypoint.sh                     # Container entrypoint
├── GNUmakefile                       # Build, image, and run targets
├── main.go                           # Provider entry point
├── go.mod / go.sum
├── internal/provider/
│   ├── provider.go
│   ├── exec.go                       # CLI tool invocation helpers
│   ├── kube.go                       # Kubernetes dynamic client helpers
│   ├── yaml_helpers.go
│   ├── resource_install_config.go
│   ├── resource_cluster.go
│   ├── resource_mirror_registry.go
│   ├── resource_image_mirror.go
│   ├── resource_catalog_source.go
│   ├── resource_subscription.go
│   ├── resource_machine_config.go
│   └── resource_machine_set.go
└── examples/
    └── bare-metal-airgapped/
        ├── main.tf
        ├── variables.tf
        ├── outputs.tf
        └── README.md
```
