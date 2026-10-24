# LockLens developer tasks. Run `make help` for a list.

GO        ?= go
NPM       ?= npm
COMPOSE   := docker compose -f deploy/compose.yaml
BIN_DIR   ?= bin
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE      ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG       := github.com/locklens/locklens/internal/buildinfo
LDFLAGS   := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

# Lab primary published by deploy/compose.yaml. Fake, local-only credentials.
export LOCKLENS_PG_PORT ?= 55432
DEMO_DSN ?= postgres://demo_app:demo-app-pw@127.0.0.1:$(LOCKLENS_PG_PORT)/locklens_demo?sslmode=disable
OBSERVER_DSN ?= postgres://locklens_observer:observer-pw@127.0.0.1:$(LOCKLENS_PG_PORT)/locklens_demo?sslmode=disable

# Lab scenario selection: make lab SCENARIO=chain DURATION=5m
SCENARIO ?= healthy
DURATION ?=

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## ---- build

.PHONY: web-install
web-install: ## Install frontend dependencies from the lockfile
	cd web && $(NPM) ci --no-audit --no-fund

.PHONY: web
web: web-install ## Build React and copy assets into the Go embed package
	cd web && $(NPM) run build
	find backend/internal/webui/dist -mindepth 1 ! -name README.txt -delete
	cp -R web/dist/. backend/internal/webui/dist/

.PHONY: backend-build
backend-build: ## Build Go binaries using the last embedded UI build
	$(GO) -C backend build -trimpath -ldflags '$(LDFLAGS)' -o $(abspath $(BIN_DIR))/locklens ./cmd/locklens
	$(GO) -C backend build -trimpath -o $(abspath $(BIN_DIR))/locklens-demo ./cmd/locklens-demo
	$(GO) -C backend build -trimpath -o $(abspath $(BIN_DIR))/locklens-lab ./cmd/locklens-lab

.PHONY: build
build: web ## Build the UI and all Go binaries, including the embedded assets
	$(MAKE) backend-build

.PHONY: build-all
build-all: build ## Alias of build

.PHONY: dev
dev: backend-build web-install ## Start lab databases, the local Go server, and Vite
	./scripts/dev.sh

.PHONY: image
image: ## Build the locklens:lab Docker image
	$(COMPOSE) build locklens

## ---- checks

.PHONY: fmt
fmt: ## Format all Go code
	gofmt -w backend tests/e2e

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-formatted
	@out=$$(gofmt -l backend tests/e2e); if [ -n "$$out" ]; then echo "Not formatted:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Run go vet (including integration-tagged files)
	$(GO) -C backend vet ./...
	$(GO) -C backend vet -tags integration ./...
	$(GO) -C tests/e2e vet -tags integration ./...

.PHONY: check-separation
check-separation: ## Verify the locklens binary does not depend on demo/lab (write) code
	@deps=$$($(GO) -C backend list -deps ./cmd/locklens) || exit $$?; \
	if printf '%s\n' "$$deps" | grep -q '/demo'; then \
		echo "cmd/locklens must not import demo packages"; exit 1; fi
	@echo "ok: cmd/locklens does not import demo packages"

.PHONY: backend-test
backend-test: ## Run Go unit tests
	$(GO) -C backend test -timeout 5m ./...

.PHONY: test
test: backend-test web-check ## Run backend and frontend unit checks

.PHONY: test-race
test-race: ## Run Go unit tests with the race detector
	$(GO) -C backend test -race -timeout 5m ./...

.PHONY: web-check
web-check: web-install ## Frontend typecheck, lint, and unit tests
	cd web && $(NPM) run typecheck && $(NPM) run lint && $(NPM) test

# PostgreSQL versions for the integration harness: 14 and the latest stable
# major, pinned to concrete image tags. Override, e.g.
#   make test-integration IT_PG_VERSIONS="14.24 15.19 16.15 17.11 18.6"
IT_PG_VERSIONS ?= 14.24 18.6

.PHONY: test-integration
test-integration: ## Run integration tests in isolated, disposable Docker Compose projects
	LOCKLENS_IT_PG_VERSIONS="$(IT_PG_VERSIONS)" ./scripts/integration-test.sh

.PHONY: test-compat
test-compat: ## Run integration tests on every supported major (14-18)
	LOCKLENS_IT_PG_VERSIONS="14.24 15.19 16.15 17.11 18.6" ./scripts/integration-test.sh

.PHONY: e2e
e2e: ## Browser end-to-end tests against an isolated full stack (Docker, Playwright)
	./scripts/e2e.sh

.PHONY: check
check: fmt-check vet check-separation backend-test test-race ## All Go checks that need no database

## ---- lab (deploy/compose.yaml, project "locklens")

.PHONY: up
up: ## Start LockLens, its store, the lab primary and standby (http://127.0.0.1:8080)
	$(COMPOSE) up -d --build --wait --wait-timeout 300

.PHONY: up-host
up-host: ## Same as up, plus the optional node_exporter
	$(COMPOSE) --profile host up -d --build --wait --wait-timeout 300

.PHONY: down
down: ## Stop the lab and delete its containers and volumes
	$(COMPOSE) --profile lab --profile host down -v --remove-orphans

.PHONY: lab
lab: ## Run a lab scenario: make lab SCENARIO=chain [DURATION=5m]
	$(COMPOSE) run --rm lab run $(SCENARIO) $(if $(DURATION),--duration $(DURATION))

.PHONY: lab-list
lab-list: ## List lab scenarios
	$(COMPOSE) run --rm lab list

.PHONY: lab-status
lab-status: ## Show lab sessions and objects
	$(COMPOSE) run --rm lab status

.PHONY: lab-stop
lab-stop: ## Stop every lab session and undo lab changes
	$(COMPOSE) run --rm lab stop

.PHONY: lab-outage
lab-outage: ## Stop the lab primary for 60 s, then start it again
	./scripts/lab-outage.sh 60

## ---- CLI demo (uses the lab primary)

.PHONY: db-up
db-up: ## Start only the lab primary (for the CLI demo)
	$(COMPOSE) up -d --wait primary

.PHONY: db-down
db-down: down ## Alias of down

.PHONY: demo
demo: backend-build db-up ## CLI demo: row lock, chain, idle in transaction against the lab primary
	@LOCKLENS_DEMO_DSN="$${LOCKLENS_DEMO_DSN:-$(DEMO_DSN)}" \
	LOCKLENS_DSN="$${LOCKLENS_DSN:-$(OBSERVER_DSN)}" \
	$(BIN_DIR)/locklens-demo --locklens "$(BIN_DIR)/locklens" all

.PHONY: doctor
doctor: backend-build db-up ## Run locklens doctor against the lab primary
	LOCKLENS_DSN='$(OBSERVER_DSN)' $(BIN_DIR)/locklens doctor

.PHONY: clean
clean: ## Remove build output
	rm -rf $(BIN_DIR) web/dist
	find backend/internal/webui/dist -mindepth 1 ! -name README.txt -delete

.PHONY: docker-smoke
docker-smoke: ## Build and verify the image and embedded UI in an isolated stack
	./scripts/docker-smoke.sh
