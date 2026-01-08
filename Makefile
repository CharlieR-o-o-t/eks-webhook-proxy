ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
BIN_DIR := bin
TEST_DIR := test
TOOLS_DIR := hack/tools
TOOLS_BIN_DIR := $(abspath $(TOOLS_DIR)/$(BIN_DIR))

export PATH := $(abspath $(TOOLS_BIN_DIR)):$(PATH)

GO_INSTALL := ./scripts/go_install.sh

REGISTRY := artifactory.wgdp.io
REPOSITORY := kind-docker

BUILD_FLAGS := $(shell hack/version.sh)

# Binaries.
GOLANGCI_LINT_BIN := golangci-lint
GOLANGCI_LINT_VER := v2.7.2 
GOLANGCI_LINT := $(abspath $(TOOLS_BIN_DIR)/$(GOLANGCI_LINT_BIN)-$(GOLANGCI_LINT_VER))
GOLANGCI_LINT_PKG := github.com/golangci/golangci-lint/v2/cmd/golangci-lint

KO_BIN := ko
KO_VER := v0.12.0
KO := $(abspath $(TOOLS_BIN_DIR)/$(KO_BIN)-$(KO_VER))
KO_PKG := github.com/google/ko

$(GOLANGCI_LINT): # Build golangci-lint from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(GOLANGCI_LINT_PKG) $(GOLANGCI_LINT_BIN) $(GOLANGCI_LINT_VER)

$(KO): # Build ko from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) $(KO_PKG) $(KO_BIN) $(KO_VER)

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Lint the codebase
	$(GOLANGCI_LINT) run -v $(GOLANGCI_LINT_EXTRA_ARGS)
	cd $(TEST_DIR); $(GOLANGCI_LINT) run --path-prefix $(TEST_DIR) -v $(GOLANGCI_LINT_EXTRA_ARGS)

.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT) ## Lint the codebase and run auto-fixers if supported by the linter
	GOLANGCI_LINT_EXTRA_ARGS=--fix $(MAKE) lint


.PHONY: ko-login
ko-login: ## Login to the registry
	ko login $(REGISTRY) -u $(KO_USERNAME) -p $(KO_PASSWORD)

.PHONY: build-image
build-image:  ## Build the server
	$(BUILD_FLAGS) ko build -B --tag-only -t $(VERSION)
