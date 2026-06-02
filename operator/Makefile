# KARO operator Makefile (kubebuilder-style).

IMG ?= ghcr.io/karo/karo-controller:latest
CONTROLLER_GEN_VERSION ?= v0.14.0
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

.PHONY: all
all: build

.PHONY: manifests
manifests: ## Generate CRDs + RBAC.
	$(CONTROLLER_GEN) crd:allowDangerousTypes=true rbac:roleName=karo-manager-role paths=./... \
		output:crd:dir=./config/crd output:rbac:dir=./config/rbac

.PHONY: generate
generate: ## Generate DeepCopy methods.
	$(CONTROLLER_GEN) object:headerFile=/dev/null paths=./api/...

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: build
build: generate fmt vet ## Build the manager binary.
	go build -o bin/manager ./cmd

.PHONY: test
test: generate fmt vet ## Run unit tests.
	go test ./... -coverprofile cover.out

.PHONY: run
run: generate fmt vet ## Run the controller locally against the current kubeconfig.
	go run ./cmd

.PHONY: docker-build
docker-build: ## Build the controller image.
	docker build -t $(IMG) .

.PHONY: install
install: manifests ## Install CRDs into the cluster.
	kubectl apply -f config/crd

.PHONY: deploy
deploy: manifests ## Deploy via Helm.
	helm upgrade --install karo charts/karo --namespace karo-system --create-namespace
