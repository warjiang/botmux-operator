SHELL := /usr/bin/env bash

IMG ?= ghcr.io/warjiang/botmux-operator:dev
CONTAINER_TOOL ?= docker
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
LOCALBIN ?= $(CURDIR)/bin
CONTROLLER_TOOLS_VERSION ?= v0.20.0
KUSTOMIZE ?= $(LOCALBIN)/kustomize
KUSTOMIZE_VERSION ?= v5.8.1
SETUP_ENVTEST ?= $(LOCALBIN)/setup-envtest
SETUP_ENVTEST_VERSION ?= release-0.24
ENVTEST_K8S_VERSION ?= 1.36.2

.PHONY: all
all: test build

.PHONY: controller-gen
controller-gen:
	@mkdir -p $(LOCALBIN)
	@test -s $(CONTROLLER_GEN) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: generate
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: manifests
manifests: controller-gen
	$(CONTROLLER_GEN) rbac:roleName=botmux-operator-manager-role crd paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

.PHONY: fmt
fmt:
	go fmt ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: test
test: manifests generate fmt vet
	go test ./... -coverprofile cover.out

.PHONY: envtest
envtest:
	@mkdir -p $(LOCALBIN)
	@test -s $(SETUP_ENVTEST) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(SETUP_ENVTEST_VERSION)
	KUBEBUILDER_ASSETS="$$($(SETUP_ENVTEST) use -p path $(ENVTEST_K8S_VERSION))" go test -tags=envtest ./internal/controller -run TestEnvtest -v

.PHONY: build
build: manifests generate fmt vet
	go build -o bin/manager ./cmd

.PHONY: docker-build
docker-build:
	$(CONTAINER_TOOL) build -t $(IMG) .

.PHONY: install
install: manifests
	kubectl apply -f config/crd/bases

.PHONY: uninstall
uninstall:
	kubectl delete -f config/crd/bases

.PHONY: deploy
deploy: manifests
	@mkdir -p $(LOCALBIN)
	@test -s $(KUSTOMIZE) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)
	$(KUSTOMIZE) build config/default | sed 's#ghcr.io/warjiang/botmux-operator:v0.1.0#$(IMG)#g' | kubectl apply -f -

.PHONY: undeploy
undeploy:
	@mkdir -p $(LOCALBIN)
	@test -s $(KUSTOMIZE) || GOBIN=$(LOCALBIN) go install sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=true -f -
