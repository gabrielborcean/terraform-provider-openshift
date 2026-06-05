#!/usr/bin/env bash
# podman-run.sh — build the podman run command with only present mounts
set -euo pipefail

IMAGE="${1:?image required}"; shift
WORKSPACE="${1:?workspace required}"; shift
INSTALL_DIR="${1:?install-dir required}"; shift
SECRETS_DIR="${1:?secrets-dir required}"; shift
# remaining args are passed as the container command (optional)

args=(
    run --rm -it
    -v "${WORKSPACE}:/workspace:Z"
    -v "${INSTALL_DIR}:/install-dir:Z"
)

if [[ -f "${SECRETS_DIR}/pull-secret.json" ]]; then
    args+=(-v "${SECRETS_DIR}/pull-secret.json:/secrets/pull-secret.json:ro,Z")
else
    echo "WARNING: ${SECRETS_DIR}/pull-secret.json not found — skipping mount" >&2
fi

if [[ -f "${SECRETS_DIR}/id_rsa.pub" ]]; then
    args+=(-v "${SECRETS_DIR}/id_rsa.pub:/secrets/ssh/id_rsa.pub:ro,Z")
else
    echo "WARNING: ${SECRETS_DIR}/id_rsa.pub not found — skipping mount" >&2
fi

if [[ -f "${SECRETS_DIR}/offline-token.txt" ]]; then
    args+=(-v "${SECRETS_DIR}/offline-token.txt:/secrets/offline-token.txt:ro,Z")
else
    echo "WARNING: ${SECRETS_DIR}/offline-token.txt not found — skipping mount" >&2
fi

if [[ -n "${PROVIDER_BIN:-}" && -f "${PROVIDER_BIN}" ]]; then
    args+=(-v "${PROVIDER_BIN}:/tmp/provider-local:ro,Z")
fi

args+=("${IMAGE}")

exec podman "${args[@]}" "$@"
