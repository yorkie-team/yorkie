/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package metadata provides metadata for RPC.
package metadata

import (
	"context"
)

// metadataKey is the key for the context.Context.
type metadataKey struct{}

// Metadata represents the metadata of the request.
type Metadata struct {
	// APIKey is the public key from the client. It is used to find the project.
	APIKey string

	// Authorization is the authorization of the request.
	Authorization string
}

// From returns the metadata from the given context.
func From(ctx context.Context) Metadata {
	return ctx.Value(metadataKey{}).(Metadata)
}

// With creates a new context with the given Metadata.
func With(ctx context.Context, md Metadata) context.Context {
	return context.WithValue(ctx, metadataKey{}, md)
}
