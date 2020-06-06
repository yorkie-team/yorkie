VERSION := 0.0.10

GIT_COMMIT := $(shell git rev-parse --short HEAD)

GO_PROJECT = github.com/yorkie-team/yorkie

GO_SRC := $(shell find . -path ./vendor -prune -o -type f -name '*.go' -print)

GO_TOOLS = \
  github.com/gogo/protobuf/proto \
  github.com/gogo/protobuf/gogoproto \
  github.com/gogo/protobuf/protoc-gen-gogo \
  github.com/gogo/protobuf/protoc-gen-gofast \
  golang.org/x/tools/cmd/goimports \
  github.com/golangci/golangci-lint \
  golang.org/x/lint/golint

GO_LDFLAGS ?=

# inject the version number into the golang version package using the -X linker flag
GO_LDFLAGS += -X ${GO_PROJECT}/pkg/version.GitCommit=${GIT_COMMIT}
GO_LDFLAGS += -X ${GO_PROJECT}/pkg/version.Version=${VERSION}

EXECUTABLE = ./bin/yorkie

tools:
	go get $(GO_TOOLS)

proto: tools
	protoc api/yorkie.proto \
-I=. \
-I=$(GOPATH)/src \
-I=$(GOPATH)/src/github.com/gogo/protobuf/protobuf \
--gofast_out=plugins=grpc,\
Mgoogle/protobuf/timestamp.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/any.proto=github.com/gogo/protobuf/types,:.

build:
	go build -o $(EXECUTABLE) -ldflags "${GO_LDFLAGS}"

docker:
	docker build -t yorkieteam/yorkie:latest .

fmt:
	gofmt -s -w $(GO_SRC)
	goimports -w -local "github.com/yorkie-team" $(GO_SRC)

lint: tools
	 golint ./...
	 go vet ./...
	 golangci-lint run ./...

test:
	go clean -testcache
	go test -race ./...

.PHONY: tools proto build fmt lint test
