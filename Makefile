.PHONY: build test cover clean sqlc help

# Build the commentcrawl CLI binary
build:
	go build -o commentcrawl ./cmd/commentcrawl

# Run all tests (no caching)
test:
	go test ./... -count=1

# Run tests with coverage report
cover:
	go test ./... -coverprofile=cover.out
	go tool cover -func=cover.out

# Remove build artifacts
clean:
	rm -f commentcrawl cover.out

# Regenerate sqlc query code from SQL files
sqlc:
	cd store && sqlc generate

# Show available targets
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  build   Build the commentcrawl CLI binary"
	@echo "  test    Run all tests (no caching)"
	@echo "  cover   Run tests with coverage report"
	@echo "  clean   Remove build artifacts"
	@echo "  sqlc    Regenerate sqlc query code from SQL files"
	@echo "  help    Show this help message"
