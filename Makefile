# LockLens developer tasks. Run `make help` for a list.

GO        ?= go
BIN_DIR   ?= bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG       := github.com/locklens/locklens/internal/buildinfo
LDFLAGS   := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

# Local demo database (see docker-compose.yml). Fake, local-only credentials.
export LOCKLENS_PG_VERSION ?= 17
export LOCKLENS_PG_PORT    ?= 55432
OBSERVER_DSN ?= postgres://locklens_observer:observer-pw@127.0.0.1:$(LOCKLENS_PG_PORT)/locklens_demo?sslmode=disable

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build bin/locklens and bin/locklens-demo
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/locklens ./cmd/locklens
	$(GO) build -trimpath -o $(BIN_DIR)/locklens-demo ./demo/cmd/locklens-demo

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-formatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "Not formatted:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet (including integration-tagged files)
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

.PHONY: check-separation
check-separation: ## Verify the CLI does not depend on the demo (write) code
	@if $(GO) list -deps ./cmd/locklens | grep -q '/demo'; then \
		echo "cmd/locklens must not import demo packages"; exit 1; fi
	@echo "ok: cmd/locklens does not import demo packages"

.PHONY: test
test: ## Run unit tests
	$(GO) test ./...

.PHONY: test-race
test-race: ## Run unit tests with the race detector
	$(GO) test -race ./...

.PHONY: test-integration
test-integration: ## Run integration tests (requires `make db-up`)
	$(GO) test -tags integration -count=1 -race -timeout 5m ./test/integration/...

.PHONY: check
check: fmt-check vet check-separation test test-race ## All checks that need no database

.PHONY: db-up
db-up: ## Start the local PostgreSQL (LOCKLENS_PG_VERSION, default 17)
	docker compose up -d --wait --renew-anon-volumes

.PHONY: db-down
db-down: ## Stop the local PostgreSQL and delete its data
	docker compose down -v

.PHONY: demo
demo: build db-up ## Run all three demo scenarios
	$(BIN_DIR)/locklens-demo all

.PHONY: demo-row-lock
demo-row-lock: build db-up ## Demo A: row lock (A -> B)
	$(BIN_DIR)/locklens-demo row-lock

.PHONY: demo-chain
demo-chain: build db-up ## Demo B: blocking chain (A -> B -> C)
	$(BIN_DIR)/locklens-demo chain

.PHONY: demo-idle
demo-idle: build db-up ## Demo C: idle in transaction
	$(BIN_DIR)/locklens-demo idle

.PHONY: doctor
doctor: build db-up ## Run locklens doctor against the local database
	LOCKLENS_DSN='$(OBSERVER_DSN)' $(BIN_DIR)/locklens doctor

.PHONY: clean
clean: ## Remove build output
	rm -rf $(BIN_DIR)
