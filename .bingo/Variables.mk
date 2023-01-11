# Auto generated binary variables helper managed by https://github.com/bwplotka/bingo v0.6. DO NOT EDIT.
# All tools are designed to be build inside $GOBIN.
BINGO_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GOPATH ?= $(shell go env GOPATH)
GOBIN  ?= $(firstword $(subst :, ,${GOPATH}))/bin
GO     ?= $(shell which go)

# Below generated variables ensure that every time a tool under each variable is invoked, the correct version
# will be used; reinstalling only if needed.
# For example for embedmd variable:
#
# In your main Makefile (for non array binaries):
#
#include .bingo/Variables.mk # Assuming -dir was set to .bingo .
#
#command: $(EMBEDMD)
#	@echo "Running embedmd"
#	@$(EMBEDMD) <flags/args..>
#
EMBEDMD := $(GOBIN)/embedmd-v1.0.0
$(EMBEDMD): $(BINGO_DIR)/embedmd.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/embedmd-v1.0.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=embedmd.mod -o=$(GOBIN)/embedmd-v1.0.0 "github.com/campoy/embedmd"

GOJSONTOYAML := $(GOBIN)/gojsontoyaml-v0.0.0-20200602132005-3697ded27e8c
$(GOJSONTOYAML): $(BINGO_DIR)/gojsontoyaml.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/gojsontoyaml-v0.0.0-20200602132005-3697ded27e8c"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=gojsontoyaml.mod -o=$(GOBIN)/gojsontoyaml-v0.0.0-20200602132005-3697ded27e8c "github.com/brancz/gojsontoyaml"

GOLANGCI_LINT := $(GOBIN)/golangci-lint-v1.45.0
$(GOLANGCI_LINT): $(BINGO_DIR)/golangci-lint.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/golangci-lint-v1.45.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=golangci-lint.mod -o=$(GOBIN)/golangci-lint-v1.45.0 "github.com/golangci/golangci-lint/cmd/golangci-lint"

JB := $(GOBIN)/jb-v0.4.0
$(JB): $(BINGO_DIR)/jb.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/jb-v0.4.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=jb.mod -o=$(GOBIN)/jb-v0.4.0 "github.com/jsonnet-bundler/jsonnet-bundler/cmd/jb"

JSONNET := $(GOBIN)/jsonnet-v0.16.0
$(JSONNET): $(BINGO_DIR)/jsonnet.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/jsonnet-v0.16.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=jsonnet.mod -o=$(GOBIN)/jsonnet-v0.16.0 "github.com/google/go-jsonnet/cmd/jsonnet"

JSONNETFMT := $(GOBIN)/jsonnetfmt-v0.16.0
$(JSONNETFMT): $(BINGO_DIR)/jsonnetfmt.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/jsonnetfmt-v0.16.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=jsonnetfmt.mod -o=$(GOBIN)/jsonnetfmt-v0.16.0 "github.com/google/go-jsonnet/cmd/jsonnetfmt"

UP := $(GOBIN)/up-v0.0.0-20220713140433-7f0630bd4890
$(UP): $(BINGO_DIR)/up.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/up-v0.0.0-20220713140433-7f0630bd4890"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=up.mod -o=$(GOBIN)/up-v0.0.0-20220713140433-7f0630bd4890 "github.com/observatorium/up/cmd/up"

