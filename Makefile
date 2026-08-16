SHELL := /bin/sh

.DEFAULT_GOAL := help

TOOLS_DIR := $(CURDIR)/.tools
LEFTHOOK_VERSION := v2.1.10
MOCKGEN_VERSION := v0.6.0
GOLANGCI_LINT_VERSION := v2.12.2
GOVULNCHECK_VERSION := v1.7.0
GITLEAKS_VERSION := v8.30.1
LOCAL_TEST_DATABASE_URL := postgres://rc:rc@localhost:5432/rc?sslmode=disable
LOAD_ENV := set -a; if [ -f .env ]; then . ./.env; fi; set +a;

.PHONY: help setup tools up down migrate migrate-down migrate-status run demo demo-down \
	single-test single-test-down all-test all-test-down test-down \
	fmt fmt-check generate generate-check lint test test-unit test-race test-integration \
	test-e2e vuln secrets build docker-build verify ci agent-preflight

help: ## Show available commands.
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: tools ## Install Git hooks and download dependencies.
	go mod download
	$(TOOLS_DIR)/lefthook install

tools: ## Download pinned Go tools.
	mkdir -p $(TOOLS_DIR)
	GOBIN=$(TOOLS_DIR) go install github.com/evilmartians/lefthook/v2@$(LEFTHOOK_VERSION)
	GOBIN=$(TOOLS_DIR) go install go.uber.org/mock/mockgen@$(MOCKGEN_VERSION)
	GOBIN=$(TOOLS_DIR) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	GOBIN=$(TOOLS_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	GOBIN=$(TOOLS_DIR) go install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)

up: ## Start PostgreSQL for local development.
	docker compose up -d --wait postgres

down: ## Stop local containers.
	docker compose down

migrate: ## Apply all database migrations.
	$(LOAD_ENV) go run ./cmd/rc migrate up

migrate-down: ## Roll back one database migration.
	$(LOAD_ENV) go run ./cmd/rc migrate down

migrate-status: ## Show database migration status.
	$(LOAD_ENV) go run ./cmd/rc migrate status

run: ## Run the API and worker process.
	$(LOAD_ENV) go run ./cmd/rc serve

demo: ## Start the containerized demo stack.
	docker compose --profile demo up --build

demo-down: ## Stop the demo stack and remove its volumes.
	docker compose --profile demo down -v

single-test: ## Start the stack and run the complete single-host end-to-end test.
	scripts/run-system-test.sh single

single-test-down: ## Stop the single-test stack and remove its local data.
	docker compose -p $${RC_SINGLE_TEST_PROJECT:-rc_abc_single_test} --profile demo down -v

all-test: ## Start an isolated stack and run the configurable large-scale test.
	scripts/run-system-test.sh all

all-test-down: ## Stop the all-test stack and remove its local data.
	docker compose -p $${RC_ALL_TEST_PROJECT:-rc_abc_all_test} --profile demo down -v

test-down: single-test-down all-test-down ## Stop both system-test stacks and remove their local data.

fmt: ## Format Go source files.
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

fmt-check: ## Fail when Go source files are not formatted.
	@test -z "$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"

generate: ## Regenerate mocks and other generated source.
	go generate ./...

generate-check: generate ## Verify generated source is current.
	git diff --exit-code

lint: ## Run static analysis.
	go vet ./...
	$(TOOLS_DIR)/golangci-lint run

test: test-unit ## Run the default test suite.

test-unit: ## Run fast unit tests.
	go test -short ./...

test-race: ## Run unit tests with the race detector.
	go test -race -short ./...

test-integration: ## Run PostgreSQL integration tests.
	RC_ENV=test go test -tags=integration ./...

test-e2e: up ## Run the complete local end-to-end test with PostgreSQL.
	RC_DATABASE_URL=$(LOCAL_TEST_DATABASE_URL) go run ./cmd/rc migrate up
	RC_ENV=test RC_DATABASE_URL=$(LOCAL_TEST_DATABASE_URL) go test -tags=e2e ./tests/e2e

vuln: ## Check Go dependencies for known vulnerabilities.
	$(TOOLS_DIR)/govulncheck ./...

secrets: ## Scan the working tree and Git history for secrets.
	$(TOOLS_DIR)/gitleaks dir --no-banner --redact --config .gitleaks.toml .
	$(TOOLS_DIR)/gitleaks git --no-banner --redact --config .gitleaks.toml .

build: ## Build application and demo binaries.
	mkdir -p bin
	go build -o bin/rc ./cmd/rc
	go build -o bin/mockreceiver ./cmd/mockreceiver

docker-build: ## Build the container image.
	docker build -t rc_abc:local .

verify: fmt-check lint test-race build ## Run the required local quality gate.
	git diff --check

ci: verify test-e2e test-integration vuln secrets docker-build ## Run the complete local quality gate.

agent-preflight: ## Show repository state before an agent task.
	scripts/agent-preflight.sh
