#!/usr/bin/env bash
# setup.sh — build the ocp-toolbox image and prepare the local environment
set -euo pipefail

# ── defaults (match GNUmakefile) ─────────────────────────────────────────────
IMAGE_NAME="${IMAGE_NAME:-ocp-toolbox}"
IMAGE_TAG="${IMAGE_TAG:-latest}"
OCP_VERSION="${OCP_VERSION:-4.14.37}"
TF_VERSION="${TF_VERSION:-1.8.5}"
MR_VERSION="${MR_VERSION:-2.0.3}"
SECRETS_DIR="${SECRETS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/secrets}"
INSTALL_DIR="${INSTALL_DIR:-$(pwd)/.install-dir}"

# ── helpers ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'; YELLOW='\033[1;33m'; GREEN='\033[0;32m'; NC='\033[0m'
info()  { echo -e "${GREEN}[setup]${NC} $*"; }
warn()  { echo -e "${YELLOW}[warn]${NC}  $*"; }
error() { echo -e "${RED}[error]${NC} $*" >&2; exit 1; }

# ── check for podman ──────────────────────────────────────────────────────────
if ! command -v podman &>/dev/null; then
    error "podman not found. Install podman: https://podman.io/getting-started/installation"
fi
info "podman $(podman --version)"

# ── verify we're in the project root ─────────────────────────────────────────
[[ -f Dockerfile && -f GNUmakefile ]] \
    || error "Run this script from the project root (where Dockerfile lives)."

# ── secrets directory ─────────────────────────────────────────────────────────
info "Checking secrets directory: ${SECRETS_DIR}"
mkdir -p "${SECRETS_DIR}"
chmod 700 "${SECRETS_DIR}"

missing=()

if [[ ! -f "${SECRETS_DIR}/pull-secret.json" ]]; then
    echo ""
    echo "  A Red Hat pull secret is required to mirror OpenShift images."
    echo "  Get yours at: https://console.redhat.com/openshift/downloads"
    echo "  Download the file, then enter its path below."
    echo ""
    read -r -e -p "  Path to pull-secret.json (or leave blank to skip): " pull_secret_path
    # expand ~ manually since read -e doesn't always do it
    pull_secret_path="${pull_secret_path/#\~/$HOME}"
    if [[ -n "${pull_secret_path}" ]]; then
        if [[ ! -f "${pull_secret_path}" ]]; then
            warn "File not found: ${pull_secret_path} — skipping."
            missing+=("pull-secret.json")
        elif ! jq -e '.auths' "${pull_secret_path}" &>/dev/null; then
            warn "File doesn't look like a valid pull secret (expected JSON with 'auths' key) — skipping."
            missing+=("pull-secret.json")
        else
            cp "${pull_secret_path}" "${SECRETS_DIR}/pull-secret.json"
            chmod 600 "${SECRETS_DIR}/pull-secret.json"
            info "Pull secret copied to ${SECRETS_DIR}/pull-secret.json"
        fi
    else
        warn "Skipped. Add it later at ${SECRETS_DIR}/pull-secret.json"
        missing+=("pull-secret.json")
    fi
fi

if [[ ! -f "${SECRETS_DIR}/id_rsa.pub" ]]; then
    missing+=("id_rsa.pub")
    warn "Missing: ${SECRETS_DIR}/id_rsa.pub"
    if [[ -f "$HOME/.ssh/id_rsa.pub" ]]; then
        read -r -p "  Copy ~/.ssh/id_rsa.pub to ${SECRETS_DIR}/id_rsa.pub? [Y/n] " ans
        if [[ "${ans:-Y}" =~ ^[Yy]$ ]]; then
            cp "$HOME/.ssh/id_rsa.pub" "${SECRETS_DIR}/id_rsa.pub"
            info "Copied ~/.ssh/id_rsa.pub → ${SECRETS_DIR}/id_rsa.pub"
            missing=("${missing[@]/id_rsa.pub}")
        fi
    else
        warn "  No ~/.ssh/id_rsa.pub found. Generate one with: ssh-keygen -t rsa -b 4096"
    fi
fi

if [[ ${#missing[@]} -gt 0 ]]; then
    warn "Some secrets are missing. The image will still build, but 'make run' will warn at startup."
fi

# ── install-dir ───────────────────────────────────────────────────────────────
info "Creating install-dir: ${INSTALL_DIR}"
mkdir -p "${INSTALL_DIR}"

# ── build image ───────────────────────────────────────────────────────────────
info "Building image ${IMAGE_NAME}:${IMAGE_TAG}"
info "  OCP_VERSION=${OCP_VERSION}  TF_VERSION=${TF_VERSION}  MR_VERSION=${MR_VERSION}"
info "  (This downloads ~1.5 GB — takes a few minutes on first run)"

podman build \
    --build-arg OCP_VERSION="${OCP_VERSION}" \
    --build-arg TERRAFORM_VERSION="${TF_VERSION}" \
    --build-arg MIRROR_REGISTRY_VERSION="${MR_VERSION}" \
    -t "${IMAGE_NAME}:${IMAGE_TAG}" \
    -f Dockerfile .

info "Image built: ${IMAGE_NAME}:${IMAGE_TAG}"

# ── smoke test ────────────────────────────────────────────────────────────────
# mirror-registry is amd64-only and segfaults under QEMU on arm64 build hosts,
# so we verify it exists rather than executing it.
info "Running smoke test..."
podman run --rm "${IMAGE_NAME}:${IMAGE_TAG}" bash -c "
    terraform version &&
    openshift-install version &&
    oc version --client &&
    test -x /usr/local/bin/mirror-registry && echo 'mirror-registry: present'
" && info "Smoke test passed." || error "Smoke test failed — check image build output above."

# ── print next steps ──────────────────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo " Setup complete. Next steps:"
echo ""
echo " 1. Copy the example config and fill in your values:"
echo "      cp -r examples/bare-metal-airgapped my-cluster"
echo "      \$EDITOR my-cluster/terraform.tfvars"
echo ""
echo " 2. Drop into an interactive shell:"
echo "      make run WORKSPACE=\$(pwd)/my-cluster"
echo ""
echo " 3. Inside the container:"
echo "      terraform init"
echo "      terraform plan"
echo "      terraform apply"
echo ""
echo " Or run non-interactively:"
echo "      make run-terraform WORKSPACE=\$(pwd)/my-cluster"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
