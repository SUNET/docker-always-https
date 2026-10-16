# ==============================================================================
# Configuration Variables
# ==============================================================================

NAME                    := always-https
VERSION                 ?= latest
DOCKER_REGISTRY         ?= docker.sunet.se
DOCKER_IMAGE            ?= $(DOCKER_REGISTRY)/$(NAME)
CURRENT_BRANCH          := $(shell git rev-parse --abbrev-ref HEAD)
GIT_VERSION             := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
RELEASE_BRANCH          ?= master

.DEFAULT_GOAL := help

.PHONY: help update gosec staticcheck vulncheck lint \
	vscode gh-install gh-auth \
	build test fmt run docker-build docker-push docker-run smoke \
	release check_current_branch

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ==============================================================================
# Go Dependency Management
# ==============================================================================

update: ## Update Go dependencies
	$(info Updating Go dependencies...)
	go get -u ./...
	go mod tidy

# ==============================================================================
# Code Quality & Security
# ==============================================================================

gosec: ## Run gosec security scanner
	$(info Running gosec)
	gosec -color -tests ./...

staticcheck: ## Run staticcheck linter
	$(info Running staticcheck)
	staticcheck ./...

vulncheck: ## Run vulnerability checker
	$(info Running vulncheck)
	govulncheck ./...

lint: staticcheck gosec vulncheck ## Run all linters

# ==============================================================================
# Development Environment
# ==============================================================================

vscode: gh-install ## Set up VS Code development environment
	$(info Installing go packages)
	go install github.com/securego/gosec/v2/cmd/gosec@latest && \
	go install golang.org/x/vuln/cmd/govulncheck@latest && \
	go install honnef.co/go/tools/cmd/staticcheck@latest && \
	go install mvdan.cc/gofumpt@latest
	$(info ==> vscode setup complete.)

gh-install: ## Install GitHub CLI (gh)
	$(info Install GitHub CLI)
	@if ! command -v gh >/dev/null 2>&1; then \
		sudo mkdir -p -m 755 /etc/apt/keyrings && \
		curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | sudo tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null && \
		sudo chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg && \
		echo "deb [arch=$$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | sudo tee /etc/apt/sources.list.d/github-cli.list > /dev/null && \
		sudo apt-get update && \
		sudo apt-get install -y gh; \
	else \
		echo "gh already installed: $$(gh --version | head -1)"; \
	fi

gh-auth: gh-install ## Authenticate GitHub CLI
	$(info Authenticate GitHub CLI)
	@if gh auth status >/dev/null 2>&1; then \
		gh auth status; \
	else \
		gh auth login; \
	fi

# ==============================================================================
# Build & Test
# ==============================================================================

build: ## Build the binary into ./bin
	$(info Building $(NAME) $(GIT_VERSION))
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(GIT_VERSION)" -o ./bin/$(NAME) .

test: ## Run all Go tests
	$(info Running tests)
	go test -race ./...

fmt: ## Format Go code with gofumpt
	gofumpt -l -w .

run: ## Run locally on unprivileged ports (pass URLS=..., ACME_URL=... as needed)
	HTTP_ADDR=:8080 HTTPS_ADDR=:8443 SSL_DIR=./ssl go run .

# ==============================================================================
# Docker
# ==============================================================================

docker-build: ## Build the Docker image (override VERSION, DOCKER_IMAGE)
	$(info Docker building $(DOCKER_IMAGE):$(VERSION))
	docker build --build-arg VERSION=$(GIT_VERSION) -t $(DOCKER_IMAGE):$(VERSION) .

docker-push: ## Push the Docker image (refuses :latest unless FORCE=true)
	@if [ "$(VERSION)" = "latest" ] && [ "$(FORCE)" != "true" ]; then \
		echo "Error: refusing to push $(DOCKER_IMAGE):$(VERSION). Set VERSION=vX.Y.Z or pass FORCE=true to override."; exit 1; \
	fi
	@echo "Pushing $(DOCKER_IMAGE):$(VERSION)"
	docker push $(DOCKER_IMAGE):$(VERSION)

docker-run: ## Run the image locally on :8080/:8443 (pass URLS=..., ACME_URL=... as needed)
	docker run --rm -p 8080:80 -p 8443:443 \
		-e URLS="$(URLS)" -e ACME_URL="$(ACME_URL)" \
		$(DOCKER_IMAGE):$(VERSION)

smoke: docker-build ## Build the image and run scripts/smoke.sh against it
	IMAGE=$(DOCKER_IMAGE):$(VERSION) bash scripts/smoke.sh

# ==============================================================================
# Release Management
# ==============================================================================

BUMP                    ?= patch
FORCE                   ?=

check_current_branch:
	$(info Current branch: $(CURRENT_BRANCH))
ifeq ($(CURRENT_BRANCH),$(RELEASE_BRANCH))
	$(info On $(RELEASE_BRANCH) branch)
else
ifneq ($(FORCE),true)
	$(error Not on $(RELEASE_BRANCH) branch — use FORCE=true to override)
else
	$(warning Not on $(RELEASE_BRANCH) branch — continuing because FORCE=true)
endif
endif

release: check_current_branch ## Build+push Docker image and create+push a git tag (BUMP=major|minor|patch)
	@echo "$(BUMP)" | grep -qE '^(major|minor|patch)$$' || \
		{ echo "Error: BUMP must be major, minor, or patch (got: $(BUMP))"; exit 1; }
	@if [ "$(FORCE)" != "true" ] && ! git diff --quiet HEAD 2>/dev/null; then \
		echo "Error: working tree is dirty — commit or stash changes first (use FORCE=true to override)"; exit 1; \
	fi
	@set -e; \
	git fetch --tags --quiet origin || true; \
	LATEST=$$(git tag -l "v*" --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -n1); \
	if [ -z "$$LATEST" ]; then \
		echo "No existing version tags found, starting at v0.0.0"; \
		LATEST="v0.0.0"; \
	fi; \
	CURRENT=$$(echo "$$LATEST" | sed 's/^v//'); \
	MAJOR=$$(echo "$$CURRENT" | cut -d. -f1); \
	MINOR=$$(echo "$$CURRENT" | cut -d. -f2); \
	PATCH=$$(echo "$$CURRENT" | cut -d. -f3); \
	case "$(BUMP)" in \
		major) MAJOR=$$((MAJOR + 1)); MINOR=0; PATCH=0 ;; \
		minor) MINOR=$$((MINOR + 1)); PATCH=0 ;; \
		patch) PATCH=$$((PATCH + 1)) ;; \
	esac; \
	NEW_TAG="v$${MAJOR}.$${MINOR}.$${PATCH}"; \
	IMAGE_TAG="$(DOCKER_IMAGE):$$NEW_TAG"; \
	IMAGE_LATEST="$(DOCKER_IMAGE):latest"; \
	echo ""; \
	echo "Bumping $$LATEST -> $$NEW_TAG ($(BUMP))"; \
	echo ""; \
	echo "==> Building Docker image $$IMAGE_TAG"; \
	docker build --build-arg VERSION="$$NEW_TAG" --tag "$$IMAGE_TAG" --tag "$$IMAGE_LATEST" .; \
	echo "==> Pushing $$IMAGE_TAG"; \
	docker push "$$IMAGE_TAG"; \
	echo "==> Pushing $$IMAGE_LATEST (-> $$NEW_TAG)"; \
	docker push "$$IMAGE_LATEST"; \
	echo ""; \
	echo "==> Tagging git $$NEW_TAG"; \
	git tag -a "$$NEW_TAG" -m "Release $$NEW_TAG"; \
	git push origin "$$NEW_TAG"; \
	echo ""; \
	echo "==> Release $$NEW_TAG created and pushed"; \
	echo ""
