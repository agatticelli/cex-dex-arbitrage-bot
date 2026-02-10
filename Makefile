.PHONY: help build docker-build start stop restart clean logs logs-detector logs-redis shell-redis status test test-integration test-coverage deps fmt lint

# Colors for output
CYAN := \033[0;36m
GREEN := \033[0;32m
YELLOW := \033[0;33m
RED := \033[0;31m
NC := \033[0m # No Color

help: ## Show this help message
	@echo "$(CYAN)CEX-DEX Arbitrage Bot - Makefile Commands$(NC)"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}'
	@echo ""

# ==============================================================================
# Development Commands
# ==============================================================================

build: ## Build the detector binary
	@echo "$(CYAN)Building detector binary...$(NC)"
	@go build -o bin/detector cmd/detector/main.go
	@echo "$(GREEN)✓ Build complete: bin/detector$(NC)"

docker-build: ## Build Docker image for detector
	@echo "$(CYAN)Building Docker image...$(NC)"
	@docker build -t arbitrage-detector:latest -f Dockerfile .
	@echo "$(GREEN)✓ Docker image built: arbitrage-detector:latest$(NC)"

start: ## Start all services including detector (Docker Compose)
	@echo "$(CYAN)Starting all services...$(NC)"
	@docker-compose up -d --build
	@echo "$(YELLOW)Waiting for services to be ready...$(NC)"
	@sleep 10
	@echo "$(GREEN)✓ All services started!$(NC)"
	@echo ""
	@echo "$(CYAN)Access URLs:$(NC)"
	@echo "  Detector:    http://localhost:8080/health"
	@echo "  Prometheus:  http://localhost:9090"
	@echo "  Jaeger UI:   http://localhost:16686"
	@echo "  Grafana:     http://localhost:3000"
	@echo ""
	@$(MAKE) status

stop: ## Stop all services
	@echo "$(CYAN)Stopping all services...$(NC)"
	@docker-compose stop
	@echo "$(GREEN)✓ Services stopped$(NC)"

restart: ## Restart all services
	@echo "$(CYAN)Restarting services...$(NC)"
	@docker-compose restart
	@echo "$(GREEN)✓ Services restarted$(NC)"

clean: ## Remove all data and volumes (full reset)
	@echo "$(RED)Stopping and removing all containers, networks, and volumes...$(NC)"
	@docker-compose down -v
	@rm -rf bin/
	@echo "$(GREEN)✓ Clean complete$(NC)"

# ==============================================================================
# Logging Commands
# ==============================================================================

logs: ## Show logs from all services
	@docker-compose logs -f

logs-detector: ## Show logs from detector service
	@docker-compose logs -f detector || echo "$(YELLOW)Detector service not running yet$(NC)"

logs-redis: ## Show logs from Redis
	@docker-compose logs -f redis

# ==============================================================================
# Shell/Debug Commands
# ==============================================================================

shell-redis: ## Open Redis CLI
	@docker-compose exec redis redis-cli

# ==============================================================================
# Status/Health Checks
# ==============================================================================

status: ## Check health of all services
	@echo "$(CYAN)Service Health Status:$(NC)"
	@echo ""

	@echo "$(YELLOW)Redis:$(NC)"
	@docker-compose exec -T redis redis-cli ping 2>/dev/null && echo "  $(GREEN)✓ Redis is healthy$(NC)" || echo "  $(RED)✗ Redis is down$(NC)"

	@echo ""
	@echo "$(YELLOW)Prometheus:$(NC)"
	@curl -s http://localhost:9090/-/healthy 2>/dev/null && echo "  $(GREEN)✓ Prometheus is healthy$(NC)" || echo "  $(RED)✗ Prometheus is down$(NC)"

	@echo ""
	@echo "$(YELLOW)Jaeger:$(NC)"
	@curl -s http://localhost:16686/ 2>/dev/null > /dev/null && echo "  $(GREEN)✓ Jaeger is healthy$(NC)" || echo "  $(RED)✗ Jaeger is down$(NC)"

	@echo ""
	@echo "$(YELLOW)Grafana:$(NC)"
	@curl -s http://localhost:3000/api/health 2>/dev/null | jq -r '.database' 2>/dev/null | grep -q "ok" && echo "  $(GREEN)✓ Grafana is healthy$(NC)" || echo "  $(RED)✗ Grafana is down$(NC)"

	@echo ""
	@echo "$(YELLOW)Detector:$(NC)"
	@curl -s http://localhost:8080/health 2>/dev/null | jq -r '.status' 2>/dev/null | grep -qE "healthy|degraded" && echo "  $(GREEN)✓ Detector is running$(NC)" || echo "  $(RED)✗ Detector is down$(NC)"

# ==============================================================================
# Testing Commands
# ==============================================================================

test: ## Run unit tests
	@echo "$(CYAN)Running unit tests...$(NC)"
	@go test -v -race -cover ./...
	@echo "$(GREEN)✓ Tests passed$(NC)"

test-integration: start ## Run integration tests
	@echo "$(CYAN)Running integration tests...$(NC)"
	@sleep 5  # Wait for services to be fully ready
	@go test -v -tags=integration ./...
	@echo "$(GREEN)✓ Integration tests passed$(NC)"

test-coverage: ## Run tests with coverage report
	@echo "$(CYAN)Running tests with coverage...$(NC)"
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "$(GREEN)✓ Coverage report generated: coverage.html$(NC)"

# ==============================================================================
# Utility Commands
# ==============================================================================

deps: ## Download Go dependencies
	@echo "$(CYAN)Downloading dependencies...$(NC)"
	@go mod download
	@go mod tidy
	@echo "$(GREEN)✓ Dependencies updated$(NC)"

fmt: ## Format Go code
	@echo "$(CYAN)Formatting code...$(NC)"
	@go fmt ./...
	@echo "$(GREEN)✓ Code formatted$(NC)"

lint: ## Run golangci-lint
	@echo "$(CYAN)Running linter...$(NC)"
	@golangci-lint run ./... || echo "$(YELLOW)Install golangci-lint: https://golangci-lint.run/$(NC)"
	@echo "$(GREEN)✓ Linting complete$(NC)"

# Default target
.DEFAULT_GOAL := help
