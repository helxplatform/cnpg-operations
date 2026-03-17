BINARY := cnpg-rest-server
CONFIG := config.json

ifneq ($(wildcard $(CONFIG)),)
REGISTRY  := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['server']['registry'])")
IMAGE     := $(shell python3 -c "import json;print(json.load(open('$(CONFIG)'))['server']['image'])")
GIT_TAG   := $(shell python3 configure.py --compute-tag)
endif

IMAGE_REF = $(REGISTRY)/$(IMAGE):$(GIT_TAG)

.PHONY: all build run test test-coverage swagger fmt vet lint tidy \
        configure docker-build docker-push deploy undeploy clean help

all: swagger build

## configure: write config.json interactively (run once before docker-build/push)
configure:
	python3 configure.py

## build: compile the server binary
build:
	CGO_ENABLED=0 go build -ldflags="-w -s" -o bin/$(BINARY) ./cmd/server

## run: run locally (requires a valid kubeconfig)
run:
	go run ./cmd/server

## test: run all unit tests with race detector
test:
	go test -race -count=1 -v ./tests/...

## test-coverage: run tests and produce coverage report
test-coverage:
	go test -race -count=1 -coverprofile=coverage.out ./tests/...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

## swagger: generate Swagger docs from annotations (requires swag CLI)
swagger:
	@which swag > /dev/null || go install github.com/swaggo/swag/cmd/swag@latest
	swag init -g cmd/server/main.go -o docs --parseDependency

## fmt: format source files
fmt:
	gofmt -w ./...

## vet: run go vet
vet:
	go vet ./...

## lint: run golangci-lint (requires golangci-lint installed)
lint:
	golangci-lint run ./...

## tidy: tidy go modules
tidy:
	go mod tidy

## docker-build: build and tag the container image (requires config.json)
docker-build: $(CONFIG) build
	docker build --platform=linux/amd64 -t $(IMAGE_REF) .

## docker-push: build and push the container image (requires config.json)
docker-push: docker-build
	docker push $(IMAGE_REF)

## deploy: apply all Kubernetes manifests
deploy:
	kubectl apply -f deploy/rbac.yaml
	kubectl apply -f deploy/deployment.yaml
	kubectl apply -f deploy/service.yaml

## undeploy: remove all Kubernetes manifests
undeploy:
	kubectl delete -f deploy/service.yaml --ignore-not-found
	kubectl delete -f deploy/deployment.yaml --ignore-not-found
	kubectl delete -f deploy/rbac.yaml --ignore-not-found

## clean: remove build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html

$(CONFIG):
	@echo "config.json not found. Run 'make configure' first."
	@exit 1

## help: print this help message
help:
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sort
