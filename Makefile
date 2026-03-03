SHELL := /bin/bash

GO ?= go
GOBUILD := $(GO) build
GOTEST := $(GO) test
GOVET := $(GO) vet
GOMOD := $(GO) mod

BINARY_NAME ?= porch
BUILD_DIR ?= bin
BINARY ?= ./$(BUILD_DIR)/$(BINARY_NAME)

CONFIG ?= ./testdata/orchestrator.e2e.yaml
BAD_CONFIG ?= ./testdata/orchestrator.badorg.yaml
STATE_FILE ?= ./testdata/.porch-state.make.json
COMPONENT ?= tektoncd-pipeline
PIPELINE ?= tp-all-in-one
WATCH_ARGS ?= --dry-run
FINAL_BRANCH ?=
COMPONENTS_FILE ?=

ifneq ($(strip $(FINAL_BRANCH)),)
FINAL_BRANCH_FLAG := --final-branch $(FINAL_BRANCH)
endif

ifneq ($(strip $(COMPONENTS_FILE)),)
COMPONENTS_FILE_FLAG := --components-file $(COMPONENTS_FILE)
endif

VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "-w -s"

.DEFAULT_GOAL := help

.PHONY: help tidy download fmt test test-coverage vet build build-local clean run-status run-retry-dry run-watch-dry integration failure-drill b02-dry b02-exec check all

help:
	@echo "Available targets:"
	@echo "  tidy           - go mod tidy"
	@echo "  download       - go mod download"
	@echo "  fmt            - go fmt ./..."
	@echo "  test           - go test ./..."
	@echo "  test-coverage  - go test with coverage report"
	@echo "  vet            - go vet ./..."
	@echo "  build          - build binary to $(BINARY)"
	@echo "  build-local    - build binary to ./$(BINARY_NAME)"
	@echo "  run-status     - run status command"
	@echo "  run-retry-dry  - run retry command in dry-run"
	@echo "  run-watch-dry  - run watch command in dry-run"
	@echo "  integration    - run integration package tests"
	@echo "  failure-drill  - run failure drill commands"
	@echo "  b02-dry        - validate B-02 in dry-run mode"
	@echo "  b02-exec       - validate B-02 in execute mode"
	@echo "  check          - test + vet + build"
	@echo "  all            - tidy + fmt + check"

tidy:
	$(GOMOD) tidy

download:
	$(GOMOD) download

fmt:
	$(GO) fmt ./...

test: tidy
	$(GOTEST) ./...

test-coverage: tidy
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

vet:
	$(GOVET) ./...

build: tidy
	mkdir -p ./$(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BINARY) ./cmd/porch

build-local: tidy
	$(GOBUILD) $(LDFLAGS) -o ./$(BINARY_NAME) ./cmd/porch

clean:
	rm -rf ./$(BUILD_DIR)
	rm -f ./$(BINARY_NAME)
	rm -f coverage.out
	rm -f ./testdata/.porch-state.*.json ./testdata/.porch-events*.log ./testdata/.interrupt.log

run-status: build
	$(BINARY) status --config $(CONFIG)

run-retry-dry: build
	$(BINARY) retry --config $(CONFIG) --component $(COMPONENT) --pipeline $(PIPELINE) --dry-run

run-watch-dry: build
	-$(BINARY) $(FINAL_BRANCH_FLAG) $(COMPONENTS_FILE_FLAG) watch --config $(CONFIG) --state-file $(STATE_FILE) $(WATCH_ARGS)

integration:
	$(GOTEST) ./pkg/integration -v

failure-drill:
	-$(GO) run ./cmd/porch status --config $(BAD_CONFIG)
	-$(GO) run ./cmd/porch watch --config $(CONFIG) --dry-run --state-file ./testdata/.porch-state.drill.json

b02-dry:
	./scripts/validate-b02.sh

b02-exec:
	./scripts/validate-b02.sh --execute

check: test vet build

all: tidy fmt check
