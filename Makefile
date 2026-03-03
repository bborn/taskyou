.PHONY: build build-no-restart build-ty build-taskd restart-daemon build-linux \
       install clean test vet vuln audit coverage run daemon \
       deploy deploy-service deploy-full status logs connect tag fmt lint

# Configuration
SERVER ?= root@cloud-claude
REMOTE_USER ?= runner
REMOTE_DIR ?= /home/runner

# Allow overriding the Go binary/toolchain (e.g. GO=go1.24.4 or mise exec -- go)
GO ?= go

# Version from git tag (e.g. v0.2.3 → 0.2.3), falls back to "dev"
VERSION ?= $(shell git describe --tags --always 2>/dev/null | sed 's/^v//' || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Build all binaries and (optionally) restart daemon if running
build: build-ty build-taskd restart-daemon

# Build binaries without touching any running daemon
build-no-restart: build-ty build-taskd

build-ty:
	$(GO) build -ldflags="$(LDFLAGS)" -o bin/ty ./cmd/task
	ln -sf ty bin/taskyou

build-taskd:
	$(GO) build -ldflags="$(LDFLAGS)" -o bin/taskd ./cmd/taskd

# Restart daemon if it's running (silent if not). Never fail the build if we lack permissions.
restart-daemon:
	@if pgrep -f "ty daemon" > /dev/null; then \
		echo "Restarting daemon..."; \
		pkill -f "ty daemon" || true; \
		sleep 1; \
		bin/ty daemon > /tmp/ty-daemon.log 2>&1 & \
		sleep 1; \
		echo "Daemon restarted (PID $$(pgrep -f 'ty daemon' || true))"; \
	fi

# Build for Linux (server deployment)
build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o bin/taskd-linux ./cmd/taskd

# Install to GOBIN (usually ~/go/bin) - installs as 'ty', 'taskyou' (symlink), and 'taskd'
install:
	go build -ldflags="$(LDFLAGS)" -o $(shell go env GOBIN)/ty ./cmd/task
	ln -sf ty $(shell go env GOBIN)/taskyou
	go build -ldflags="$(LDFLAGS)" -o $(shell go env GOBIN)/taskd ./cmd/taskd

# Clean build artifacts
clean:
	rm -rf bin/ dist/

# Run tests with race detector
test:
	go test -race ./...

# Run go vet
vet:
	go vet ./...

# Run govulncheck
vuln:
	govulncheck ./...

# Run all checks: vet, vuln, lint, test
audit: vet vuln lint test

# Generate HTML coverage report
coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Run the TUI locally (ty command)
run:
	go run ./cmd/task

# Run the daemon locally
daemon:
	go run ./cmd/taskd

# Deploy binary to server
deploy: build-linux
	@echo "Deploying to $(SERVER)..."
	scp bin/taskd-linux $(SERVER):$(REMOTE_DIR)/taskd
	ssh $(SERVER) 'chmod +x $(REMOTE_DIR)/taskd && chown $(REMOTE_USER):$(REMOTE_USER) $(REMOTE_DIR)/taskd'
	@echo "Restarting service..."
	-ssh $(SERVER) 'systemctl restart taskd'
	@echo "Done! Connect with: ssh -p 2222 cloud-claude"

# Install systemd service on server (first time only)
deploy-service:
	./scripts/install-service.sh $(SERVER) $(REMOTE_USER) $(REMOTE_DIR)

# Full deployment (first time)
deploy-full: build-linux
	@echo "Deploying binary..."
	scp bin/taskd-linux $(SERVER):$(REMOTE_DIR)/taskd
	ssh $(SERVER) 'chmod +x $(REMOTE_DIR)/taskd && chown $(REMOTE_USER):$(REMOTE_USER) $(REMOTE_DIR)/taskd'
	@echo "Installing service..."
	./scripts/install-service.sh $(SERVER) $(REMOTE_USER) $(REMOTE_DIR)

# Check server status
status:
	ssh $(SERVER) 'systemctl status taskd'

# View server logs
logs:
	ssh $(SERVER) 'journalctl -u taskd -f'

# Connect to the TUI
connect:
	ssh -p 2222 cloud-claude

# Create a new release (usage: make tag VERSION=v0.1.0)
tag:
ifndef VERSION
	$(error VERSION is required. Usage: make tag VERSION=v0.1.0)
endif
	@echo "Creating release $(VERSION)..."
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)
	@echo "Done! GitHub Actions will build and publish the release."
	@echo "View at: https://github.com/bborn/taskyou/releases/tag/$(VERSION)"

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

.DEFAULT_GOAL := build
