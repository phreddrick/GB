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
#   make help         - Show this help message

.PHONY: all gb-server gb-client makeuniv racegen clean test help

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
