# =============================================================================
# Variables
# =============================================================================

GOCACHE    := $(CURDIR)/build-cache
DEBUG_DIR  := bin/debug
DIST_DIR   := bin/dist
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
DIST_FLAGS := -trimpath -ldflags="-s -w -X github.com/stanier/stoa/internal/version.Version=$(VERSION)"
GO         := GOCACHE=$(GOCACHE) go

DOCKER_REGISTRY ?= ghcr.io/stanier
IMAGE_TAG       ?= latest

# All binaries — name matches ./cmd/<name> and bin/debug/<name>
BINARIES    := chat mcp-filesystem mcp-memory dump-context mcp-time mcp-fetch mcp-attachments mcp-diff mcp-web-search mcp-http mcp-graphql mcp-netbox mcp-promql mcp-zulip mcp-shell mcp-trello mcp-acp agentd auth-admin authd local-idp webd zulip-bridge slack-bridge discord-bridge matrix-bridge cli-bridge inference-gateway apikeyd pii-filter
MCP_SERVERS := mcp-filesystem mcp-memory mcp-time mcp-fetch mcp-attachments mcp-diff mcp-web-search mcp-http mcp-graphql mcp-netbox mcp-promql mcp-zulip mcp-shell mcp-trello mcp-acp

.DEFAULT_GOAL := help

# =============================================================================
# Build macros
# =============================================================================

define go-build
mkdir -p $(DEBUG_DIR) && $(GO) build -o $(DEBUG_DIR)/$(1) ./cmd/$(1)
endef

define go-dist
mkdir -p $(DIST_DIR) && $(GO) build $(DIST_FLAGS) -o $(DIST_DIR)/$(1) ./cmd/$(1)
endef

# Generates build-<name> and dist-<name> for every entry in BINARIES
define BINARY_TEMPLATE
.PHONY: build-$(1) dist-$(1)
build-$(1):
	$$(call go-build,$(1))
dist-$(1):
	$$(call go-dist,$(1))
endef

$(foreach b,$(BINARIES),$(eval $(call BINARY_TEMPLATE,$(b))))

# =============================================================================
# Debug builds (bin/debug/ with symbols)
# =============================================================================

##@ Debug builds (bin/debug/ — with symbols)

.PHONY: build build-mcp-all build-web dev run

build: $(addprefix build-,$(BINARIES)) ## Build all binaries  (individual: build-<name>, see list-binaries)

build-mcp-all: $(addprefix build-,$(MCP_SERVERS)) ## Build all MCP server binaries

build-web: ## Build Svelte frontend into internal/webd/dist/ (required before build-webd)
	cd web && npm run build

# wire web build as a prerequisite for both debug and dist webd targets
build-webd: build-web
dist-webd: build-web

dev: ## Build chat with race detector
	mkdir -p $(DEBUG_DIR)
	$(GO) build -race -o $(DEBUG_DIR)/chat ./cmd/chat

run: build-chat ## Build and run the chat client
	./$(DEBUG_DIR)/chat

# =============================================================================
# Distribution builds (bin/dist/ stripped + trimpath)
# =============================================================================

##@ Distribution builds (bin/dist/ — stripped + trimpath)

.PHONY: dist dist-mcp-all

dist: $(addprefix dist-,$(BINARIES)) ## Build all binaries stripped  (individual: dist-<name>, see list-binaries)

dist-mcp-all: $(addprefix dist-,$(MCP_SERVERS)) ## Build all MCP server binaries (stripped)

# =============================================================================
# Utility
# =============================================================================

##@ Utility

.PHONY: init-data-dirs clean test deps install fmt lint upx upx-all list-binaries help

init-data-dirs: ## Create runtime data directories and seed default squid config
	mkdir -p data/squid data/logs data/traces data/sessions
	@if [ ! -f data/squid/squid.conf ]; then \
		cp config/squid.default.conf data/squid/squid.conf; \
		echo "Seeded data/squid/squid.conf from config/squid.default.conf"; \
	fi

clean: ## Remove build artifacts
	rm -rf bin

test: ## Run tests
	$(GO) test ./...

deps: ## Download and tidy dependencies
	go mod download
	go mod tidy

install: build-chat ## Install chat binary to /usr/local/bin/stoa
	sudo cp $(DIST_DIR)/chat /usr/local/bin/stoa

fmt: ## Format code
	go fmt ./...

lint: ## Lint code (requires golangci-lint)
	golangci-lint run

upx: dist-chat  ## Compress chat binary with UPX (~50-70% smaller, slower startup)
	@if command -v upx >/dev/null 2>&1; then \
		upx --best --lzma $(DIST_DIR)/chat; \
	else \
		echo "UPX not found. Install with: sudo apt-get install upx-ucl"; \
		exit 1; \
	fi

upx-all: dist-all ## Compress all dist binaries with UPX
	@if command -v upx >/dev/null 2>&1; then \
		upx --best --lzma $(DIST_DIR)/*; \
	else \
		echo "UPX not found. Install with: sudo apt-get install upx-ucl"; \
		exit 1; \
	fi

list-binaries: ## List all individual build-<name> / dist-<name> targets
	@echo $(BINARIES) | tr ' ' '\n' | awk '{printf "  build-%-28s dist-%s\n", $$1, $$1}'

# =============================================================================
# Docker
# =============================================================================

##@ Docker

.PHONY: docker-build-all docker-build-mcp-all docker-build-chat
.PHONY: docker-push-all docker-compose-up docker-compose-down
.PHONY: $(addprefix docker-build-,$(MCP_SERVERS))

docker-build-all: docker-build-chat docker-build-mcp-all ## Build all Docker images

docker-build-mcp-all: $(addprefix docker-build-,$(MCP_SERVERS)) ## Build all MCP server Docker images

docker-build-chat: ## Build chat TUI Docker image
	docker build --target chat -t $(DOCKER_REGISTRY)/stoa:$(IMAGE_TAG) -t $(DOCKER_REGISTRY)/stoa:latest .


$(addprefix docker-build-,$(MCP_SERVERS)): docker-build-%:
	docker build --target $* -t $(DOCKER_REGISTRY)/stoa-$*:$(IMAGE_TAG) -t $(DOCKER_REGISTRY)/stoa-$*:latest .

docker-push-all: ## Push all images to registry
	docker push $(DOCKER_REGISTRY)/stoa:$(IMAGE_TAG)
	docker push $(DOCKER_REGISTRY)/stoa:latest
	for s in $(MCP_SERVERS); do \
		docker push $(DOCKER_REGISTRY)/stoa-$$s:$(IMAGE_TAG); \
		docker push $(DOCKER_REGISTRY)/stoa-$$s:latest; \
	done

docker-compose-up: ## Start services with docker-compose
	docker-compose up -d

docker-compose-down: ## Stop services with docker-compose
	docker-compose down

# =============================================================================
# Podman — local builds (always tagged :local)
# =============================================================================

##@ Podman — local builds

# Service images (non-MCP) — used by podman-build-all, podman-clean, podman-push-all
SERVICES := chat agentd auth-admin authd local-idp webd zulip-bridge slack-bridge discord-bridge matrix-bridge inference-gateway apikeyd pii-filter

.PHONY: podman-build-all podman-build-mcp-all
.PHONY: $(addprefix podman-build-,$(SERVICES))
.PHONY: $(addprefix podman-rebuild-,$(SERVICES))
.PHONY: $(addprefix podman-build-,$(MCP_SERVERS))
.PHONY: podman-compose-up podman-compose-rebuild podman-compose-down podman-clean

podman-build-all: $(addprefix podman-build-,$(SERVICES)) podman-build-mcp-all ## Build all images tagged :local

podman-build-mcp-all: $(addprefix podman-build-,$(MCP_SERVERS)) ## Build all MCP server images tagged :local

$(addprefix podman-build-,$(SERVICES)): podman-build-%:
	podman build --target $* -t $*:local .

# podman-rebuild-<name>: force rebuild without layer cache (use after Go source changes)
$(addprefix podman-rebuild-,$(SERVICES)): podman-rebuild-%:
	podman build --no-cache --target $* -t $*:local .

$(addprefix podman-build-,$(MCP_SERVERS)): podman-build-%:
	podman build --target $* -t $*:local .

podman-compose-up: ## Start services with podman-compose (no rebuild)
	podman-compose -f docker-compose.podman.yml up -d

podman-compose-rebuild: ## Rebuild compose images then restart services
	podman-compose -f docker-compose.podman.yml down
	podman-compose -f docker-compose.podman.yml build
	podman-compose -f docker-compose.podman.yml up -d

podman-compose-down: ## Stop services with podman-compose
	podman-compose -f docker-compose.podman.yml down

podman-clean: ## Remove all :local tagged project images
	podman rmi --ignore \
		$(foreach s,$(SERVICES),$(s):local) \
		$(foreach s,$(MCP_SERVERS),$(s):local)

# =============================================================================
# Podman — registry push (retag :local → registry name, then push)
# =============================================================================

##@ Podman — registry

.PHONY: podman-push-all podman-push-mcp-all
.PHONY: $(addprefix podman-push-,$(SERVICES))
.PHONY: $(addprefix podman-push-,$(MCP_SERVERS))

# retag-and-push IMAGE LOCAL_NAME REGISTRY_NAME
# Retags LOCAL_NAME:local as REGISTRY_NAME:$(IMAGE_TAG) and pushes it.
define retag-and-push
	podman tag $(1):local $(2):$(IMAGE_TAG)
	podman push $(2):$(IMAGE_TAG)
endef

# chat gets a distinct registry name; all others are stoa-<name>
define push-service
podman-push-$(1):
	$$(call retag-and-push,$(1),$$(DOCKER_REGISTRY)/stoa-$(1))
endef

podman-push-all: $(addprefix podman-push-,$(SERVICES)) podman-push-mcp-all ## Retag all :local images and push to $(DOCKER_REGISTRY)

podman-push-mcp-all: $(addprefix podman-push-,$(MCP_SERVERS)) ## Retag and push all MCP server images to $(DOCKER_REGISTRY)

podman-push-chat: ## Retag chat:local and push to registry
	$(call retag-and-push,chat,$(DOCKER_REGISTRY)/stoa)

$(foreach s,$(filter-out chat,$(SERVICES)),$(eval $(call push-service,$(s))))

$(addprefix podman-push-,$(MCP_SERVERS)): podman-push-%:
	$(call retag-and-push,$*,$(DOCKER_REGISTRY)/stoa-$*)

# =============================================================================
# Help
# =============================================================================

help: ## Show this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)}' $(MAKEFILE_LIST)
