# Galactic Bloodshed (Go port) - Makefile
#
# Targets:
#   make              - Build all binaries
#   make gb-server    - Build main game server
#   make gb-client    - Build game client
#   make makeuniv     - Build universe initializer
#   make racegen      - Build player enrollment
#   make clean        - Remove all binaries
#   make test         - Run all tests
#   make docker-build - Build Docker image
#   make docker-run   - Run server in Docker (testing)
#   make k8s-deploy   - Deploy to Kubernetes
#   make k8s-delete   - Delete Kubernetes deployment
#   make help         - Show this help message

.PHONY: all gb-server gb-client makeuniv racegen clean test docker-build docker-run k8s-deploy k8s-delete help

# Build configuration
GO := go
GOFLAGS := -v
BINDIR := .

# Build all binaries
all: gb-server gb-client makeuniv racegen

# Main game server
gb-server:
	@echo "Building gb-server..."
	$(GO) build $(GOFLAGS) -o $(BINDIR)/gb-server ./cmd/gb-server

# Game client
gb-client:
	@echo "Building gb-client..."
	$(GO) build $(GOFLAGS) -o $(BINDIR)/gb-client ./cmd/gb-client

# Universe initializer
makeuniv:
	@echo "Building makeuniv..."
	$(GO) build $(GOFLAGS) -o $(BINDIR)/makeuniv ./cmd/makeuniv

# Player enrollment
racegen:
	@echo "Building racegen..."
	$(GO) build $(GOFLAGS) -o $(BINDIR)/racegen ./cmd/racegen

# Clean up binaries
clean:
	@echo "Cleaning..."
	rm -f $(BINDIR)/gb-server $(BINDIR)/gb-client $(BINDIR)/makeuniv $(BINDIR)/racegen
	$(GO) clean

# Run tests
test:
	@echo "Running tests..."
	$(GO) test -v ./...

# Show help
help:
	@echo "Galactic Bloodshed - Go Build System"
	@echo ""
	@grep -E '^#   make' $(MAKEFILE_LIST) | sed 's/^#   //'

# ─── Docker Targets ────────────────────────────────────────────────────────

# Build Docker image
docker-build:
	@echo "Building Docker image gb:latest..."
	cd .. && docker build -t gb:latest .
	@echo "Docker image built successfully!"

# Run server in Docker (for testing)
docker-run: docker-build
	@echo "Starting GB server in Docker..."
	docker run -it --rm \
		-p 8080:8080 \
		-v gb-data:/data \
		gb:latest

# ─── Kubernetes Targets ────────────────────────────────────────────────────

# Deploy to Kubernetes
k8s-deploy: docker-build
	@echo "Deploying to Kubernetes..."
	@if command -v kubectl >/dev/null 2>&1; then \
		kubectl apply -f ../k8s-deployment.yaml; \
		echo "Deployment applied. Check status with: kubectl get pods"; \
	else \
		echo "Error: kubectl not found. Install kubectl first."; \
		exit 1; \
	fi

# Load image into minikube
k8s-minikube-load: docker-build
	@echo "Loading image into minikube..."
	@if command -v minikube >/dev/null 2>&1; then \
		minikube image load gb:latest; \
		echo "Image loaded. Deploy with: make k8s-deploy"; \
	else \
		echo "Error: minikube not found."; \
		exit 1; \
	fi

# Delete Kubernetes deployment
k8s-delete:
	@echo "Deleting Kubernetes deployment..."
	@if command -v kubectl >/dev/null 2>&1; then \
		kubectl delete -f ../k8s-deployment.yaml; \
		echo "Deployment deleted."; \
	else \
		echo "Error: kubectl not found."; \
		exit 1; \
	fi

# Show deployment logs
k8s-logs:
	@if command -v kubectl >/dev/null 2>&1; then \
		kubectl logs -f deployment/gb-server -c gb-server; \
	else \
		echo "Error: kubectl not found."; \
		exit 1; \
	fi

# Show Kubernetes status
k8s-status:
	@if command -v kubectl >/dev/null 2>&1; then \
		echo "=== Deployment ==="; \
		kubectl get deployment gb-server; \
		echo ""; \
		echo "=== Pods ==="; \
		kubectl get pods -l app=gb-server; \
		echo ""; \
		echo "=== Service ==="; \
		kubectl get svc gb-server; \
	else \
		echo "Error: kubectl not found."; \
		exit 1; \
	fi
