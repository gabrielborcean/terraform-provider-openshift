#!/usr/bin/env bash
# publish.sh — one-time setup for publishing to the Terraform Registry
set -euo pipefail

RED='\033[0;31m'; YELLOW='\033[1;33m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${GREEN}[publish]${NC} $*"; }
warn()  { echo -e "${YELLOW}[warn]${NC}  $*"; }
error() { echo -e "${RED}[error]${NC} $*" >&2; exit 1; }
step()  { echo -e "\n${CYAN}── $* ${NC}"; }

# ── config ────────────────────────────────────────────────────────────────────
GITHUB_ORG="${GITHUB_ORG:-gabrielborcean}"
REPO_NAME="terraform-provider-openshift"
GPG_KEY_FILE="gpg-public-key.asc"
GPG_PRIVATE_KEY_FILE="gpg-private-key.asc"
TAG="${1:-v0.1.0}"

# ── checks ────────────────────────────────────────────────────────────────────
[[ -f GNUmakefile && -f .goreleaser.yml ]] \
    || error "Run this script from the project root."

for cmd in git gpg gh; do
    command -v "$cmd" &>/dev/null || error "'$cmd' not found. Install it first.
  gpg:  brew install gnupg
  gh:   brew install gh"
done

# GITHUB_TOKEN env var (injected by make publish) takes precedence over gh keychain
if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    export GH_TOKEN="${GITHUB_TOKEN}"
fi

gh auth status &>/dev/null || error "Not logged in to GitHub. Run: gh auth login"

# ── step 1: github repo ───────────────────────────────────────────────────────
step "1/5  GitHub repository"

if gh repo view "${GITHUB_ORG}/${REPO_NAME}" &>/dev/null; then
    info "Repo ${GITHUB_ORG}/${REPO_NAME} already exists — skipping creation."
else
    info "Creating public repo ${GITHUB_ORG}/${REPO_NAME}..."
    gh repo create "${GITHUB_ORG}/${REPO_NAME}" --public --description \
        "Terraform provider for airgapped on-premises OpenShift on bare metal"
fi

# Always use HTTPS with token — SSH agent is not available inside the container
HTTPS_REMOTE="https://${GH_TOKEN:-${GITHUB_TOKEN}}@github.com/${GITHUB_ORG}/${REPO_NAME}.git"
if ! git remote get-url origin &>/dev/null; then
    git remote add origin "${HTTPS_REMOTE}"
    info "Remote 'origin' added (HTTPS)."
else
    git remote set-url origin "${HTTPS_REMOTE}"
    info "Remote 'origin' updated to HTTPS."
fi

# ── step 2: initial commit + push ────────────────────────────────────────────
step "2/5  Push code"

if [[ -z "$(git log --oneline 2>/dev/null | head -1)" ]]; then
    info "No commits yet — creating initial commit..."
    git add .
    git commit -m "Initial commit"
fi

CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
info "Pushing branch '${CURRENT_BRANCH}' to origin..."
git push -u origin "${CURRENT_BRANCH}"

# ── step 3: gpg key ───────────────────────────────────────────────────────────
step "3/5  GPG signing key"

# If private key file exists (from a prior run), import it so the container
# keyring has it — needed when running inside podman on macOS where the host
# GPG agent socket is not forwarded.
if [[ -f "${GPG_PRIVATE_KEY_FILE}" ]]; then
    info "Importing GPG key from ${GPG_PRIVATE_KEY_FILE}..."
    gpg --batch --import "${GPG_PRIVATE_KEY_FILE}" 2>/dev/null || true
fi

GPG_KEY_ID=$(gpg --list-secret-keys --keyid-format LONG 2>/dev/null \
    | grep -E "^sec" | head -1 | awk '{print $2}' | cut -d'/' -f2 || true)

[[ -n "${GPG_KEY_ID}" ]] || error "No GPG secret key found — run make publish once from the host (outside the container) to generate gpg-private-key.asc, then retry."
info "Using GPG key: ${GPG_KEY_ID}"

info "Exporting GPG key ${GPG_KEY_ID}..."
gpg --armor --export "${GPG_KEY_ID}" > "${GPG_KEY_FILE}"
gpg --armor --export-secret-keys "${GPG_KEY_ID}" > "${GPG_PRIVATE_KEY_FILE}"
chmod 600 "${GPG_PRIVATE_KEY_FILE}"
info "Public key:  ${GPG_KEY_FILE}"
info "Private key: ${GPG_PRIVATE_KEY_FILE}  (keep this safe, do not commit)"

# ── step 4: github secrets ────────────────────────────────────────────────────
step "4/5  GitHub Actions secrets"

info "Setting GPG_PRIVATE_KEY secret..."
gh secret set GPG_PRIVATE_KEY \
    --repo "${GITHUB_ORG}/${REPO_NAME}" \
    < "${GPG_PRIVATE_KEY_FILE}"

# Passphrase — only needed if key has one
if gpg --batch --pinentry-mode loopback --passphrase "" \
        --sign /dev/null -o /dev/null &>/dev/null 2>&1; then
    info "GPG key has no passphrase — setting PASSPHRASE to empty string."
    gh secret set PASSPHRASE \
        --repo "${GITHUB_ORG}/${REPO_NAME}" \
        --body ""
else
    read -r -s -p "  Enter GPG passphrase (for PASSPHRASE secret): " gpg_pass
    echo ""
    printf '%s' "${gpg_pass}" | gh secret set PASSPHRASE \
        --repo "${GITHUB_ORG}/${REPO_NAME}"
fi

info "Secrets set."

# ── step 5: tag + push ────────────────────────────────────────────────────────
step "5/5  Tag and release"

if git tag | grep -qx "${TAG}"; then
    warn "Tag ${TAG} already exists locally — skipping tag creation."
else
    git tag "${TAG}"
    info "Created tag ${TAG}"
fi

git push origin "${TAG}"
info "Pushed tag ${TAG} — GitHub Actions will build and publish the release."

# ── next steps ────────────────────────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo " Almost done. One manual step remains:"
echo ""
echo " 1. Go to https://registry.terraform.io"
echo "    → Sign in with GitHub"
echo "    → Publish → Provider → ${REPO_NAME}"
echo ""
echo " 2. When prompted for a GPG key, paste the contents of:"
echo "    ${GPG_KEY_FILE}"
echo ""
cat "${GPG_KEY_FILE}"
echo ""
echo " 3. Watch the release build:"
echo "    https://github.com/${GITHUB_ORG}/${REPO_NAME}/actions"
echo ""
echo " Once the registry detects the release, use the provider with:"
echo ""
echo '   terraform {'
echo '     required_providers {'
echo '       openshift = {'
echo "         source  = \"${GITHUB_ORG}/openshift\""
echo '         version = "~> 0.1"'
echo '       }'
echo '     }'
echo '   }'
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
