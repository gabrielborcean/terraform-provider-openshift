HOSTNAME=registry.terraform.io
NAMESPACE=gabrielborcean
NAME=openshift
BINARY=terraform-provider-${NAME}
VERSION=$(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.0-dev")
OS_ARCH=$(shell go env GOOS)_$(shell go env GOARCH)

IMAGE_NAME   ?= ocp-toolbox
IMAGE_TAG    ?= latest
OCP_VERSION  ?= 4.14.37
TF_VERSION   ?= 1.8.5
MR_VERSION   ?= 2.0.3

# Mount points — override on the command line
WORKSPACE    ?= $(CURDIR)/examples/multi-site
CUSTOMER     ?= acme
ENV          ?= dev
INSTALL_DIR  ?= $(CURDIR)/.install-dir
SECRETS_DIR  ?= $(CURDIR)/secrets

default: help

.PHONY: help
help:
	@echo ""
	@echo "  NEW HERE? Start with:  make setup"
	@echo ""
	@echo "  ── first time ───────────────────────────────────────────────"
	@echo "  make setup             Check prerequisites, show missing secrets"
	@echo "  make image             Build the ocp-toolbox container image (do once)"
	@echo ""
	@echo "  ── multi-site (recommended) ─────────────────────────────────"
	@echo "  make site-apply   CUSTOMER=acme ENV=dev    Deploy an environment"
	@echo "  make site-plan    CUSTOMER=acme ENV=dev    Plan changes"
	@echo "  make site-destroy CUSTOMER=acme ENV=prod   Tear down an environment"
	@echo "  make site-list                             List all workspaces"
	@echo ""
	@echo "  Workspace = CUSTOMER-ENV (e.g. acme-dev, globex-prod)"
	@echo "  Var-files loaded: workspaces/CUSTOMER/common.tfvars + workspaces/CUSTOMER/ENV.tfvars"
	@echo ""
	@echo "  ── single deploy ────────────────────────────────────────────"
	@echo "  make plan              terraform plan  (review before applying)"
	@echo "  make run-local         Build provider from source + terraform apply"
	@echo "  make run-registry      Pull provider from registry + terraform apply"
	@echo "  make destroy           terraform destroy (tear down cluster + infra)"
	@echo "  make shell             Interactive shell inside the container"
	@echo ""
	@echo "  ── validate ─────────────────────────────────────────────────"
	@echo "  make validate          terraform validate (syntax + schema check)"
	@echo "  make test-registry     Smoke-test: pull provider from registry, init only"
	@echo ""
	@echo "  ── release ──────────────────────────────────────────────────"
	@echo "  make publish           Build + push a signed GitHub release (set RELEASE_TAG)"
	@echo ""
	@echo "  ── airgapped mirror ─────────────────────────────────────────"
	@echo "  make install-mirror    Stage provider binary into filesystem mirror layout"
	@echo "                         (/usr/local/lib/tf-plugins) for airgapped bastions"
	@echo "  MIRROR_VERSION=0.4.16  Override version to stage (default: current tag)"
	@echo ""
	@echo "  ── development ──────────────────────────────────────────────"
	@echo "  make build             Build provider binary locally (requires Go)"
	@echo "  make install           Install provider to ~/.terraform.d/plugins/"
	@echo "  make test              Run unit tests"
	@echo "  make testacc           Run acceptance tests (requires live cluster)"
	@echo "  make fmt               Format Go source"
	@echo "  make lint              Run golangci-lint"
	@echo "  make docs              Regenerate provider docs with tfplugindocs"
	@echo "  make clean             Remove built binary"
	@echo ""

.PHONY: setup
setup:
	@echo ""
	@echo "=== terraform-provider-openshift setup ==="
	@echo ""
	@command -v podman >/dev/null 2>&1 || { echo "ERROR: podman not found — install podman first"; exit 1; }
	@echo "✓ podman found"
	@mkdir -p secrets
	@touch secrets/.gitkeep
	@echo ""
	@echo "Place the following files in ./secrets/ before running make image:"
	@echo ""
	@echo "  secrets/pull-secret.json   — download from https://console.redhat.com/openshift/downloads"
	@echo "  secrets/id_rsa.pub         — your SSH public key (ssh-keygen -t rsa if needed)"
	@echo "  secrets/offline-token.txt  — get from https://console.redhat.com/openshift/token"
	@echo ""
	@for f in pull-secret.json id_rsa.pub offline-token.txt; do \
	  if [ -f "secrets/$$f" ]; then \
	    echo "  ✓ secrets/$$f"; \
	  else \
	    echo "  ✗ secrets/$$f  (MISSING)"; \
	  fi; \
	done
	@echo ""
	@echo "Once all secrets are present:"
	@echo ""
	@echo "  1. Fill in your values:"
	@echo "     cp test-assisted/terraform.tfvars.example test-assisted/terraform.tfvars"
	@echo "     \$$EDITOR test-assisted/terraform.tfvars"
	@echo ""
	@echo "  2. Build the image and deploy:"
	@echo "     make image"
	@echo "     make run-registry WORKSPACE=\$$(pwd)/test-assisted"
	@echo ""

.PHONY: build
build:
	go build -o ${BINARY} .

.PHONY: install
install: build
	mkdir -p ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}
	mv ${BINARY} ~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${NAME}/${VERSION}/${OS_ARCH}

.PHONY: test
test:
	go test ./... -v $(TESTARGS) -timeout 120s

.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m

.PHONY: test-integration
test-integration:
	TF_ACC=1 go test ./... -run TestAcc -v -timeout 60m

.PHONY: fmt
fmt:
	gofmt -s -w .

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: generate
generate:
	go generate ./...

.PHONY: docs
docs:
	$(shell go env GOPATH)/bin/tfplugindocs generate --provider-name openshift

.PHONY: clean
clean:
	rm -f ${BINARY}

PUBLISH_IMAGE ?= ocp-publish
RELEASE_TAG   ?= v$(VERSION)

# ── container targets ─────────────────────────────────────────────────────────

.PHONY: publish-image
publish-image:
	podman build \
	  -t $(PUBLISH_IMAGE):latest \
	  -f Dockerfile.publish .

.PHONY: publish
publish: publish-image
	podman run --rm -it \
	  -v $(CURDIR):/repo:Z \
	  -e GITHUB_TOKEN=$(shell gh auth token) \
	  -e GITHUB_ORG=$(NAMESPACE) \
	  $(PUBLISH_IMAGE):latest $(RELEASE_TAG)

.PHONY: image
image:
	podman build \
	  --build-arg OCP_VERSION=$(OCP_VERSION) \
	  --build-arg TERRAFORM_VERSION=$(TF_VERSION) \
	  --build-arg MIRROR_REGISTRY_VERSION=$(MR_VERSION) \
	  --build-arg PROVIDER_VERSION=$(VERSION) \
	  -t $(IMAGE_NAME):$(IMAGE_TAG) \
	  -f Dockerfile . && touch .image-built


.PHONY: shell
shell: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)"

.PHONY: run
run: shell

.PHONY: plan
plan: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "rm -f .terraform.lock.hcl && terraform init && terraform plan"

.PHONY: destroy
destroy: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "terraform destroy"

.PHONY: validate
validate: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "rm -f .terraform.lock.hcl && terraform init -backend=false && terraform validate"

# run-local: build provider locally, inject into container, then apply (no registry needed)
.PHONY: run-local
run-local: _ensure-dirs
	GOARCH=amd64 GOOS=linux go build -o $(CURDIR)/.provider-local .
	@PROVIDER_BIN=$(CURDIR)/.provider-local \
	  scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "ARCH=\$$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && \
	    PLUGIN_DIR=/usr/local/lib/tf-plugins/registry.terraform.io/gabrielborcean/openshift/99.0.0/linux_\$$ARCH && \
	    mkdir -p \$$PLUGIN_DIR && \
	    cp /tmp/provider-local \$$PLUGIN_DIR/terraform-provider-openshift_v99.0.0 && \
	    chmod +x \$$PLUGIN_DIR/terraform-provider-openshift_v99.0.0 && \
	    rm -f .terraform.lock.hcl && \
	    terraform init && \
	    terraform apply"

# run-registry: pulls provider from registry.terraform.io, runs terraform apply
.PHONY: run-registry
run-registry: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "unset TF_CLI_ARGS_init && rm -f .terraform.lock.hcl && terraform init && terraform apply"

.PHONY: run-terraform
run-terraform: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "terraform init && terraform apply"

.PHONY: test-registry
test-registry:
	mkdir -p $(CURDIR)/test
	@if [ ! -f $(CURDIR)/test/main.tf ]; then \
	  printf 'terraform {\n  required_providers {\n    openshift = {\n      source  = "gabrielborcean/openshift"\n      version = "~> 0.1"\n    }\n  }\n}\nprovider "openshift" {}\n' \
	    > $(CURDIR)/test/main.tf; \
	fi
	podman run --rm \
	  -v $(CURDIR)/test:/workspace:Z \
	  $(IMAGE_NAME):$(IMAGE_TAG) \
	  bash -c "unset TF_CLI_ARGS_init && terraform init && echo '--- PROVIDER PULL OK ---'"

# ── multi-site workspace targets ──────────────────────────────────────────────
# Usage: make site-plan    CUSTOMER=acme ENV=dev
#        make site-apply   CUSTOMER=acme ENV=dev
#        make site-destroy CUSTOMER=acme ENV=prod
#        make site-list
#
# Workspace name = CUSTOMER-ENV  (e.g. acme-dev, globex-prod)
# Loads: workspaces/CUSTOMER/common.tfvars  +  workspaces/CUSTOMER/ENV.tfvars

_WORKSPACE_NAME = $(CUSTOMER)-$(ENV)
_VAR_FILES      = -var-file=workspaces/$(CUSTOMER)/common.tfvars -var-file=workspaces/$(CUSTOMER)/$(ENV).tfvars

.PHONY: site-plan
site-plan: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "unset TF_CLI_ARGS_init && rm -f .terraform.lock.hcl && terraform init && \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform workspace select $(_WORKSPACE_NAME) 2>/dev/null || \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform workspace new $(_WORKSPACE_NAME) && \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform plan $(_VAR_FILES)"

.PHONY: site-apply
site-apply: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "unset TF_CLI_ARGS_init && rm -f .terraform.lock.hcl && terraform init && \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform workspace select $(_WORKSPACE_NAME) 2>/dev/null || \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform workspace new $(_WORKSPACE_NAME) && \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform apply $(_VAR_FILES)"

.PHONY: site-destroy
site-destroy: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "unset TF_CLI_ARGS_init && terraform init && \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform workspace select $(_WORKSPACE_NAME) && \
	    TF_WORKSPACE=$(_WORKSPACE_NAME) terraform destroy $(_VAR_FILES)"

.PHONY: site-list
site-list: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "unset TF_CLI_ARGS_init && terraform init -backend=false 2>/dev/null && terraform workspace list"

# ── airgapped filesystem mirror ───────────────────────────────────────────────
# Stages the provider binary into the layout Terraform expects for a filesystem
# mirror. Run this on an internet-connected machine, then rsync the result to
# your airgapped bastion (or bake it into the ocp-toolbox image).
#
# Usage:
#   make install-mirror                      # uses current git tag
#   make install-mirror MIRROR_VERSION=0.4.16
#
# Output: /usr/local/lib/tf-plugins/registry.terraform.io/gabrielborcean/openshift/<ver>/linux_amd64/

MIRROR_VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//')
MIRROR_DIR     = /usr/local/lib/tf-plugins/registry.terraform.io/gabrielborcean/openshift

.PHONY: install-mirror
install-mirror:
	@if [ -z "$(MIRROR_VERSION)" ]; then \
	  echo "ERROR: no git tag found. Set MIRROR_VERSION=x.y.z"; exit 1; fi
	@echo "→ Staging provider v$(MIRROR_VERSION) into filesystem mirror..."
	go build -o terraform-provider-openshift_v$(MIRROR_VERSION) .
	mkdir -p $(MIRROR_DIR)/$(MIRROR_VERSION)/linux_amd64
	cp terraform-provider-openshift_v$(MIRROR_VERSION) \
	   $(MIRROR_DIR)/$(MIRROR_VERSION)/linux_amd64/terraform-provider-openshift_v$(MIRROR_VERSION)
	rm terraform-provider-openshift_v$(MIRROR_VERSION)
	@echo "✓ Staged: $(MIRROR_DIR)/$(MIRROR_VERSION)/linux_amd64/"
	@echo ""
	@echo "  Next steps for airgapped bastion:"
	@echo "  1. rsync -av /usr/local/lib/tf-plugins bastion:/usr/local/lib/"
	@echo "  2. cp examples/multi-site/.terraformrc.airgapped ~/.terraformrc"

.PHONY: _ensure-dirs
_ensure-dirs:
	mkdir -p $(INSTALL_DIR) $(SECRETS_DIR)
