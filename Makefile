.PHONY: build install clean test deploy web

# Configuration
SERVER ?= root@cloud-claude
REMOTE_USER ?= runner
REMOTE_DIR ?= /home/runner

# Build all binaries and restart daemon if running
build: build-task build-taskd restart-daemon

build-task:
	go build -o bin/task ./cmd/task

build-taskd:
	go build -o bin/taskd ./cmd/taskd

build-taskweb:
	go build -o bin/taskweb ./cmd/taskweb

# Build web frontend
web:
	cd web && npm install && npm run build

# Build taskweb with embedded frontend
build-taskweb-full: web build-taskweb

# Run web UI in development mode (connects to local database)
# Usage: make webdev (run API server), then in another terminal: make webui
webdev:
	go run ./cmd/taskweb-dev

webui:
	cd web && npm install && npm run dev

# Restart daemon if it's running (silent if not)
restart-daemon:
	@if pgrep -f "task daemon" > /dev/null; then \
		echo "Restarting daemon..."; \
		pkill -f "task daemon"; \
		sleep 1; \
		bin/task daemon > /tmp/task-daemon.log 2>&1 & \
		sleep 1; \
		echo "Daemon restarted (PID $$(pgrep -f 'task daemon'))"; \
	fi

# Build for Linux (server deployment)
build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/taskd-linux ./cmd/taskd

# Install to GOBIN (usually ~/go/bin)
install:
	go install ./cmd/task
	go install ./cmd/taskd

# Clean build artifacts
clean:
	rm -rf bin/

# Run tests
test:
	go test ./...

# Run the TUI locally
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

# Build for release (all platforms)
release:
	GOOS=darwin GOARCH=amd64 go build -o bin/task-darwin-amd64 ./cmd/task
	GOOS=darwin GOARCH=arm64 go build -o bin/task-darwin-arm64 ./cmd/task
	GOOS=linux GOARCH=amd64 go build -o bin/task-linux-amd64 ./cmd/task
	GOOS=linux GOARCH=amd64 go build -o bin/taskd-linux-amd64 ./cmd/taskd

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run

.DEFAULT_GOAL := build
