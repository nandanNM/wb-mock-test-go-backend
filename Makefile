GO      ?= go
BINARY  ?= server
PKG     := ./cmd/server
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
IMAGE   ?= backend:latest

.PHONY: run build test vet fmt tidy clean docker-build docker-run

run: ## Run the server (reads .env)
	$(GO) run $(PKG)

build: ## Build the binary into ./bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

test: ## Run tests with the race detector
	$(GO) test -race ./...

vet: ## Static analysis
	$(GO) vet ./...

fmt: ## Format code
	$(GO) fmt ./...

tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf bin

docker-build: ## Build the Docker image
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE) .

docker-run: ## Run the image (pass DATABASE_URL via your shell or --env-file .env)
	docker run --rm -p 8080:8080 --env-file .env $(IMAGE)
