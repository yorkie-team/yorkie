EXECUTABLE = ./bin/yorkie

GOSRC := $(shell find . -path ./vendor -prune -o -type f -name '*.go' -print)

GOTOOLS = \
  github.com/gogo/protobuf/proto \
  github.com/gogo/protobuf/gogoproto \
  github.com/gogo/protobuf/protoc-gen-gogo \
  github.com/gogo/protobuf/protoc-gen-gofast \
  golang.org/x/tools/cmd/goimports \
  github.com/golangci/golangci-lint

tools:
	go get $(GOTOOLS)

proto: tools
	protoc api/yorkie.proto \
-I=. \
-I=$(GOPATH)/src \
-I=$(GOPATH)/src/github.com/gogo/protobuf/protobuf \
--gofast_out=plugins=grpc,\
Mgoogle/protobuf/timestamp.proto=github.com/gogo/protobuf/types,\
Mgoogle/protobuf/any.proto=github.com/gogo/protobuf/types,:.

build: proto
	go build -o $(EXECUTABLE)

fmt:
	gofmt -w $(GOSRC)
	goimports -w -local "github.com/yorkie-team" $(GOSRC)

lint:
	 golint ./...
	 go vet ./...
	 golangci-lint run ./...

test:
	go clean -testcache
	go test -race ./...

.PHONY: tools proto build fmt lint test
