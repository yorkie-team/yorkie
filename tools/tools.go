// +build tools

// This file ensures tool dependencies are kept in sync. This is the
// recommended way of doing this according to
// https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module

// To install the following tools at the version used by this repo run:
// $ make tools
// or
// $ go generate -tags tools tools/tools.go

package tools

//go:generate go install github.com/golang/protobuf/proto
import _ "github.com/golang/protobuf/proto"

//go:generate go install github.com/gogo/protobuf/gogoproto
import _ "github.com/gogo/protobuf/gogoproto"

//go:generate go install github.com/gogo/protobuf/protoc-gen-gogo
import _ "github.com/gogo/protobuf/protoc-gen-gogo"

//go:generate go install github.com/gogo/protobuf/protoc-gen-gofast
import _ "github.com/gogo/protobuf/protoc-gen-gofast"

//go:generate go install github.com/golangci/golangci-lint/cmd/golangci-lint
import _ "github.com/golangci/golangci-lint/cmd/golangci-lint"
