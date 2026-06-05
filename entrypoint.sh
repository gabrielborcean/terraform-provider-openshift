#!/usr/bin/env bash
set -euo pipefail

# ── resolve runtime arch for the plugin dir ───────────────────────────────────
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
PLUGIN_BASE="/usr/local/lib/tf-plugins/registry.terraform.io/gabrielborcean/openshift/0.1.0/linux_${ARCH}"
export TF_CLI_ARGS_init="-plugin-dir=/usr/local/lib/tf-plugins"

# ── warn about expected mounts ────────────────────────────────────────────────
if [[ ! -f /secrets/pull-secret.json ]]; then
    echo "WARNING: /secrets/pull-secret.json not found — mount with -v /path/to/pull-secret.json:/secrets/pull-secret.json:ro"
fi

if [[ -z "$(ls -A /workspace 2>/dev/null)" ]]; then
    echo "WARNING: /workspace is empty — mount your Terraform config with -v /path/to/config:/workspace:Z"
fi

# ── convenience env vars when secrets are present ────────────────────────────
[[ -f /secrets/pull-secret.json ]] && export OPENSHIFT_PULL_SECRET_FILE=/secrets/pull-secret.json
[[ -f /secrets/ssh/id_rsa.pub  ]] && export OPENSHIFT_SSH_KEY=$(cat /secrets/ssh/id_rsa.pub)
[[ -f /workspace/kubeconfig    ]] && export KUBECONFIG=/workspace/kubeconfig

echo "ocp-toolbox ready"
echo "  terraform       $(terraform version -json | jq -r '.terraform_version')"
echo "  openshift-install $(openshift-install version | head -1)"
echo "  oc              $(oc version --client 2>/dev/null | head -1)"
echo "  oc-mirror       $(oc-mirror version 2>&1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+[^[:space:]]*' | head -1 || echo "installed")"
echo "  mirror-registry $(ls -la /usr/local/bin/mirror-registry 2>/dev/null | awk '{print $NF, "(installed)"}' || echo "NOT FOUND")"
echo "  provider plugin ${PLUGIN_BASE}"
echo ""

exec "$@"
