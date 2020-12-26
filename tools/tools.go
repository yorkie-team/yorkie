// +build tools

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
