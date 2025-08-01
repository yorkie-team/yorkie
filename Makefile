YORKIE_VERSION := 0.6.23

GO_PROJECT = github.com/yorkie-team/yorkie

ifeq ($(OS),Windows_NT)
    BUILD_DATE := $(shell echo %date:~6,4%-%date:~0,2%-%date:~3,2%)
    GO_SRC := $(shell dir /s /b *.go | findstr /v "vendor")
    EXECUTABLE = ./bin/yorkie.exe
else
    BUILD_DATE := $(shell date "+%Y-%m-%d")
    GO_SRC := $(shell find . -path ./vendor -prune -o -type f -name '*.go' -print)
    EXECUTABLE = ./bin/yorkie
endif

# inject the version number into the golang version package using the -X linker flag
GO_LDFLAGS ?=
GO_LDFLAGS += -X ${GO_PROJECT}/internal/version.Version=${YORKIE_VERSION}
GO_LDFLAGS += -X ${GO_PROJECT}/internal/version.BuildDate=${BUILD_DATE}

default: help

tools: ## install tools for developing yorkie
	go install github.com/bufbuild/buf/cmd/buf@v1.28.1
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.31.0
	go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.12.0
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.6
	go install github.com/sudorandom/protoc-gen-connect-openapi@v0.5.5

proto: ## generate proto files
	buf generate

build: ## builds an executable that runs in the current environment
	CGO_ENABLED=0 go build -o $(EXECUTABLE) -ldflags "${GO_LDFLAGS}" ./cmd/yorkie

build-binaries: ## builds binaries to attach a new release
	rm -rf binaries
	./build/build-binaries.sh $(YORKIE_VERSION) "$(GO_LDFLAGS)"

fmt: ## applies format and simplify codes
	gofmt -s -w $(GO_SRC)

lint: ## runs the golang-ci lint, checks for lint violations
	golangci-lint run ./...

coverage: ## runs coverage tests
	go clean -testcache
	go test -tags integration -race -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt
	rm -f coverage.txt

test: ## runs integration tests that require local applications such as MongoDB
	go clean -testcache
	go test -tags integration -race ./...

test-complex: ## runs complex tests that take a long time
	go clean -testcache
	go test -tags complex -race -v ./test/complex/...

bench: ## runs benchmark tests
	rm -f pipe output.txt mem.prof cpu.prof bench.test
	mkfifo pipe
	tee output.txt < pipe &
	go test -tags bench -benchmem -bench=. ./test/bench -memprofile=mem.prof -cpuprofile=cpu.prof > pipe
	rm -f pipe

docker: ## builds docker images with the current version and latest tag
	docker buildx build --push --platform linux/amd64,linux/arm64,linux/386 -t yorkieteam/yorkie:$(YORKIE_VERSION) -t yorkieteam/yorkie:latest .

docker-latest: ## builds docker images with latest tag
	docker buildx build --push --platform linux/amd64,linux/arm64,linux/386 -t yorkieteam/yorkie:latest .

start: ## runs the server in the background and redirects output to a log file
	CGO_ENABLED=0 go build -o $(EXECUTABLE) -ldflags "${GO_LDFLAGS}" ./cmd/yorkie
	./bin/yorkie server --mongo-connection-uri mongodb://localhost:27017 --pprof-enabled > yorkie-server.log 2>&1 &
	@echo "Server is running in background. Check logs in yorkie-server.log"
	@echo "To stop the server, run: pkill -f 'yorkie server'"

stop: ## stops the server
	@echo "Stopping server..."
	@pkill -f 'yorkie server'
	@echo "Server stopped."

swagger: ## runs swagger-ui with the yorkie api docs
	docker run -p 3000:8080 \
  		-e URLS="[ \
			{ url: 'docs/yorkie/v1/admin.openapi.yaml', name: 'Admin' }, \
			{ url: 'docs/yorkie/v1/resources.openapi.yaml', name: 'Resources' }, \
			{ url: 'docs/yorkie/v1/yorkie.openapi.yaml', name: 'Yorkie' }  \
		]" \
		-v `pwd`/api/docs:/usr/share/nginx/html/docs/ \
  		swaggerapi/swagger-ui

help:
	@echo 'Commands:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "    %-20s %s\n", $$1, $$2}'
	@echo

.PHONY: tools proto build build-binaries fmt lint test bench docker docker-latest start stop swagger help
