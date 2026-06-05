HOSTNAME=registry.terraform.io
NAMESPACE=gabrielborcean
NAME=openshift
BINARY=terraform-provider-${NAME}
VERSION=0.1.0
OS_ARCH=$(shell go env GOOS)_$(shell go env GOARCH)

IMAGE_NAME   ?= ocp-toolbox
IMAGE_TAG    ?= latest
OCP_VERSION  ?= 4.14.37
TF_VERSION   ?= 1.8.5
MR_VERSION   ?= 2.0.3

# Mount points — override on the command line
WORKSPACE    ?= $(CURDIR)/examples/bare-metal-airgapped
INSTALL_DIR  ?= $(CURDIR)/.install-dir
SECRETS_DIR  ?= $(CURDIR)/secrets

default: help

.PHONY: help
help:
	@echo ""
	@echo "  NEW HERE? Start with:  make setup"
	@echo ""
	@echo "  make setup             Check prerequisites and place secrets"
	@echo "  make image             Build the ocp-toolbox container image (do this once)"
	@echo "  make run-local         Build provider from source + run terraform apply"
	@echo "  make run-registry      Pull provider from registry + run terraform apply"
	@echo "  make run               Interactive shell inside the container"
	@echo ""
	@echo "  make build             Build the provider binary locally (requires Go)"
	@echo "  make install           Install provider to ~/.terraform.d/plugins/"
	@echo "  make test              Run unit tests"
	@echo "  make testacc           Run acceptance tests (requires live cluster)"
	@echo "  make fmt               Format Go source"
	@echo "  make lint              Run golangci-lint"
	@echo "  make docs              Regenerate provider docs"
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
	@echo "Once all secrets are present, run:"
	@echo ""
	@echo "  make image"
	@echo "  make run-registry WORKSPACE=\$$(pwd)/test-assisted"
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
RELEASE_TAG   ?= v0.1.0

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
	  -t $(IMAGE_NAME):$(IMAGE_TAG) \
	  -f Dockerfile .


.PHONY: run
run: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)"

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
	    terraform apply \
	      -var=\"offline_token=\$$(cat /secrets/offline-token.txt)\" \
	      -var=\"pull_secret=\$$(cat /secrets/pull-secret.json)\" \
	      -var=\"ssh_public_key=\$$(cat /secrets/ssh/id_rsa.pub)\""

# run-registry: pulls provider from registry.terraform.io, runs terraform apply
.PHONY: run-registry
run-registry: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "unset TF_CLI_ARGS_init && \
	    rm -f .terraform.lock.hcl && \
	    terraform init && \
	    terraform apply \
	      -var=\"offline_token=\$$(cat /secrets/offline-token.txt)\" \
	      -var=\"pull_secret=\$$(cat /secrets/pull-secret.json)\" \
	      -var=\"ssh_public_key=\$$(cat /secrets/ssh/id_rsa.pub)\""

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

.PHONY: _ensure-dirs
_ensure-dirs:
	mkdir -p $(INSTALL_DIR) $(SECRETS_DIR)
