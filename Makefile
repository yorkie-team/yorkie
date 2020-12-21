YORKIE_VERSION := 0.1.1

GIT_COMMIT := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date "+%Y-%m-%d")
GO_PROJECT = github.com/yorkie-team/yorkie
GO_SRC := $(shell find . -path ./vendor -prune -o -type f -name '*.go' -print)

# inject the version number into the golang version package using the -X linker flag
GO_LDFLAGS ?=
GO_LDFLAGS += -X ${GO_PROJECT}/pkg/version.GitCommit=${GIT_COMMIT}
GO_LDFLAGS += -X ${GO_PROJECT}/pkg/version.Version=${YORKIE_VERSION}
GO_LDFLAGS += -X ${GO_PROJECT}/pkg/version.BuildDate=${BUILD_DATE}

EXECUTABLE = ./bin/yorkie

tools:
	go generate -tags tools tools/tools.go

proto:
	protoc api/yorkie.proto \
-I=. \
-I=$(GOPATH)/src \
-I=$(GOPATH)/src/github.com/gogo/protobuf/protobuf \
--gofast_out=plugins=grpc,\
Mgoogle/protobuf/timestamp.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/any.proto=github.com/gogo/protobuf/types,:.

build:
	go build -o $(EXECUTABLE) -ldflags "${GO_LDFLAGS}" ./cmd/yorkie

docker:
	docker build -t yorkieteam/yorkie:$(YORKIE_VERSION) -t yorkieteam/yorkie:latest .

fmt:
	gofmt -s -w $(GO_SRC)

lint:
	 golangci-lint run ./...

test:
	go clean -testcache
	go test -race ./...

.PHONY: tools proto build docker fmt lint test
