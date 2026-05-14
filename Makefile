# Copyright The Linux Foundation and each contributor to LFX.
# SPDX-License-Identifier: MIT

GO_MODULE=github.com/linuxfoundation/lfx-v2-email-service
CMD_PATH=./cmd/email-service
BINARY_NAME=email-service
BINARY_PATH=bin/$(BINARY_NAME)

GO_FILES=$(shell find . -name '*.go' -not -path './vendor/*')

BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_COMMIT=$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -X main.BuildTime=$(BUILD_TIME) -X main.GitCommit=$(GIT_COMMIT)"

TEST_FLAGS=-race
TEST_TIMEOUT=5m

DOCKER_IMAGE=ghcr.io/linuxfoundation/lfx-v2-email-service/email-service
DOCKER_TAG=latest

HELM_CHART_PATH=./charts/lfx-v2-email-service
HELM_RELEASE_NAME=lfx-v2-email-service
HELM_NAMESPACE=lfx
HELM_LOCAL_VALUES_FILE=values.local.yaml

.PHONY: all
all: clean fmt lint test build

.PHONY: deps
deps:
	@echo "==> Downloading dependencies..."
	@go mod download

.PHONY: build
build: clean
	@echo "==> Building $(BINARY_NAME)..."
	@mkdir -p bin
	go build $(LDFLAGS) -o $(BINARY_PATH) $(CMD_PATH)
	@echo "==> Build complete: $(BINARY_PATH)"

.PHONY: run
run:
	@echo "==> Running $(BINARY_NAME)..."
	go run $(LDFLAGS) $(CMD_PATH)

.PHONY: test
test:
	@echo "==> Running tests..."
	go test $(TEST_FLAGS) -timeout $(TEST_TIMEOUT) ./...

.PHONY: test-verbose
test-verbose:
	go test $(TEST_FLAGS) -v -timeout $(TEST_TIMEOUT) ./...

.PHONY: test-coverage
test-coverage:
	@mkdir -p coverage
	go test $(TEST_FLAGS) -cover -timeout $(TEST_TIMEOUT) -coverprofile=coverage/coverage.out ./...
	go tool cover -html=coverage/coverage.out -o coverage/coverage.html

.PHONY: clean
clean:
	@rm -rf bin/ coverage/

.PHONY: lint
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found. Install via: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

.PHONY: fmt
fmt:
	@go fmt ./...
	@gofmt -s -w $(GO_FILES)

.PHONY: check
check:
	@if [ -n "$$(gofmt -l $(GO_FILES))" ]; then \
		echo "Files need formatting:"; gofmt -l $(GO_FILES); exit 1; \
	fi
	@$(MAKE) lint
	@$(MAKE) license-check

.PHONY: license-check
license-check:
	@missing=$$(find . -name "*.go" -not -path "./vendor/*" \
		-exec sh -c 'head -10 "$$1" | grep -q "Copyright The Linux Foundation" || echo "$$1"' _ {} \;); \
	if [ -n "$$missing" ]; then echo "Missing license headers:"; echo "$$missing"; exit 1; fi

.PHONY: docker-build
docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -f Dockerfile .

.PHONY: helm-install
helm-install:
	helm upgrade --force --install $(HELM_RELEASE_NAME) $(HELM_CHART_PATH) --namespace $(HELM_NAMESPACE)

.PHONY: helm-install-local
helm-install-local:
	helm upgrade --force --install $(HELM_RELEASE_NAME) $(HELM_CHART_PATH) --namespace $(HELM_NAMESPACE) \
		--values $(HELM_CHART_PATH)/$(HELM_LOCAL_VALUES_FILE)

.PHONY: helm-templates
helm-templates:
	helm template $(HELM_RELEASE_NAME) $(HELM_CHART_PATH) --namespace $(HELM_NAMESPACE)

.PHONY: helm-uninstall
helm-uninstall:
	helm uninstall $(HELM_RELEASE_NAME) --namespace $(HELM_NAMESPACE)

.PHONY: helm-restart
helm-restart:
	kubectl rollout restart deployment/$(HELM_RELEASE_NAME) --namespace $(HELM_NAMESPACE)
