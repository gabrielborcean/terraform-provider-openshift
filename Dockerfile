# syntax=docker/dockerfile:1
#
# Build:
#   podman build -t ocp-toolbox:latest .
#   podman build --build-arg OCP_VERSION=4.15.12 -t ocp-toolbox:4.15 .
#
# Run (podman):
#   podman run --rm -it \
#     -v ./terraform-config:/workspace:Z \
#     -v ./install-dir:/install-dir:Z \
#     -v ./pull-secret.json:/secrets/pull-secret.json:ro,Z \
#     -v ~/.ssh/id_rsa.pub:/secrets/ssh/id_rsa.pub:ro,Z \
#     ocp-toolbox:latest

# ── versions ──────────────────────────────────────────────────────────────────
ARG GO_VERSION=1.21
ARG TERRAFORM_VERSION=1.8.5
ARG OCP_VERSION=4.14.37
ARG MIRROR_REGISTRY_VERSION=2.0.3

# ── stage 1: build the Terraform provider ────────────────────────────────────
FROM docker.io/library/golang:${GO_VERSION}-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/terraform-provider-openshift .

# ── stage 2: runtime ─────────────────────────────────────────────────────────
FROM docker.io/library/debian:bookworm-slim

ARG TERRAFORM_VERSION
ARG OCP_VERSION
ARG MIRROR_REGISTRY_VERSION

ENV DEBIAN_FRONTEND=noninteractive \
    TF_PLUGIN_CACHE_DIR=/root/.terraform.d/plugin-cache \
    KUBECONFIG=/workspace/kubeconfig \
    PATH="/usr/local/bin:${PATH}"

# ── system packages ───────────────────────────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        jq \
        make \
        skopeo \
        unzip \
        wget \
    && rm -rf /var/lib/apt/lists/*

# ── Terraform ─────────────────────────────────────────────────────────────────
RUN set -eux; \
    ARCH=$(dpkg --print-architecture); \
    curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${ARCH}.zip" \
        -o /tmp/terraform.zip; \
    unzip -q /tmp/terraform.zip -d /usr/local/bin; \
    rm /tmp/terraform.zip; \
    terraform version

# ── openshift-install ─────────────────────────────────────────────────────────
RUN set -eux; \
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
    curl -fsSL "https://mirror.openshift.com/pub/openshift-v4/${ARCH}/clients/ocp/${OCP_VERSION}/openshift-install-linux.tar.gz" \
        -o /tmp/oi.tar.gz; \
    tar -C /usr/local/bin -xzf /tmp/oi.tar.gz openshift-install; \
    rm /tmp/oi.tar.gz; \
    openshift-install version

# ── oc + kubectl ──────────────────────────────────────────────────────────────
RUN set -eux; \
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
    curl -fsSL "https://mirror.openshift.com/pub/openshift-v4/${ARCH}/clients/ocp/${OCP_VERSION}/openshift-client-linux.tar.gz" \
        -o /tmp/oc.tar.gz; \
    tar -C /usr/local/bin -xzf /tmp/oc.tar.gz oc kubectl; \
    rm /tmp/oc.tar.gz; \
    oc version --client

# ── oc-mirror ─────────────────────────────────────────────────────────────────
RUN set -eux; \
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/'); \
    curl -fsSL "https://mirror.openshift.com/pub/openshift-v4/${ARCH}/clients/ocp/${OCP_VERSION}/oc-mirror.tar.gz" \
        -o /tmp/ocm.tar.gz; \
    tar -C /usr/local/bin -xzf /tmp/ocm.tar.gz oc-mirror; \
    chmod +x /usr/local/bin/oc-mirror; \
    rm /tmp/ocm.tar.gz

# ── mirror-registry (Quay) ────────────────────────────────────────────────────
# Asset has no arch suffix; tag requires "v" prefix.
# Skip --version: the amd64 binary segfaults under QEMU (arm64 build host).
RUN set -eux; \
    curl -fsSL "https://github.com/quay/mirror-registry/releases/download/v${MIRROR_REGISTRY_VERSION}/mirror-registry-online.tar.gz" \
        -o /tmp/mr.tar.gz; \
    tar -C /usr/local/bin -xzf /tmp/mr.tar.gz mirror-registry; \
    rm /tmp/mr.tar.gz; \
    test -x /usr/local/bin/mirror-registry

# ── install provider into local filesystem plugin dir ─────────────────────────
# Terraform resolves local providers from TF_CLI_ARGS_init / -plugin-dir, or
# from ~/.terraform.d/plugins. We install to the well-known local path so the
# workspace's main.tf doesn't need a network registry.
COPY --from=builder /out/terraform-provider-openshift \
    /usr/local/lib/tf-plugins/registry.terraform.io/r2dts/openshift/0.1.0/linux_amd64/terraform-provider-openshift_v0.1.0

# Symlink for arm64 so the same image works on both arches
RUN set -eux; \
    ARCH=$(dpkg --print-architecture); \
    if [ "${ARCH}" != "amd64" ]; then \
        mkdir -p "/usr/local/lib/tf-plugins/registry.terraform.io/r2dts/openshift/0.1.0/linux_${ARCH}"; \
        ln -sf \
            /usr/local/lib/tf-plugins/registry.terraform.io/r2dts/openshift/0.1.0/linux_amd64/terraform-provider-openshift_v0.1.0 \
            "/usr/local/lib/tf-plugins/registry.terraform.io/r2dts/openshift/0.1.0/linux_${ARCH}/terraform-provider-openshift_v0.1.0"; \
    fi

# ── workspace dirs ────────────────────────────────────────────────────────────
RUN mkdir -p /root/.terraform.d/plugin-cache /workspace /secrets /install-dir

# /workspace  — mount your Terraform config here
# /secrets    — mount pull-secret.json and SSH keys here (read-only)
# /install-dir — mount the openshift-install working directory here

WORKDIR /workspace

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["bash"]
