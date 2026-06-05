HOSTNAME=registry.terraform.io
NAMESPACE=r2dts
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

default: build

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
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs

.PHONY: clean
clean:
	rm -f ${BINARY}

# ── container targets ─────────────────────────────────────────────────────────

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

.PHONY: run-terraform
run-terraform: _ensure-dirs
	@scripts/podman-run.sh "$(IMAGE_NAME):$(IMAGE_TAG)" "$(WORKSPACE)" "$(INSTALL_DIR)" "$(SECRETS_DIR)" \
	  bash -c "terraform init && terraform apply"

.PHONY: _ensure-dirs
_ensure-dirs:
	mkdir -p $(INSTALL_DIR) $(SECRETS_DIR)
