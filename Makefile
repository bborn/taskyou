.PHONY: build build-no-restart install clean test deploy

# Configuration
SERVER ?= root@cloud-claude
REMOTE_USER ?= runner
REMOTE_DIR ?= /home/runner

# Allow overriding the Go binary/toolchain (e.g. GO=go1.24.4 or mise exec -- go)
GO ?= go

# Build all binaries and (optionally) restart daemon if running
build: build-ty build-taskd restart-daemon

# Build binaries without touching any running daemon
build-no-restart: build-ty build-taskd

build-ty:
	$(GO) build -o bin/ty ./cmd/task

build-taskd:
	$(GO) build -o bin/taskd ./cmd/taskd

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
	GOOS=linux GOARCH=amd64 go build -o bin/taskd-linux ./cmd/taskd

# Install to GOBIN (usually ~/go/bin) - installs as 'ty' and 'taskd'
install:
	go build -o $(shell go env GOBIN)/ty ./cmd/task
	go install ./cmd/taskd

# Clean build artifacts
clean:
	rm -rf bin/

# Run tests
test:
	go test ./...

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

# Build for release (all platforms)
release:
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/ty-darwin-amd64 ./cmd/task
	GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o bin/ty-darwin-arm64 ./cmd/task
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/ty-linux-amd64 ./cmd/task
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/ty-linux-arm64 ./cmd/task
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/taskd-linux-amd64 ./cmd/taskd
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bin/taskd-linux-arm64 ./cmd/taskd

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

.DEFAULT_GOAL := build
