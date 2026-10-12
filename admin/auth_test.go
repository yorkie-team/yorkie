/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package admin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// TestAuthInterceptorToken pins the token handling of the admin
// AuthInterceptor, which mirrors the client package's: a
// token swapped by SetToken while other goroutines issue requests is read
// whole -- the race detector flags a plain-field swap here -- and an
// interceptor built without NewAuthInterceptor carries the empty token
// instead of panicking.
func TestAuthInterceptorToken(t *testing.T) {
	t.Run("swap under concurrent requests", func(t *testing.T) {
		tokens := map[string]bool{"initial": true}
		for i := range 8 {
			tokens[fmt.Sprintf("token-%d", i)] = true
		}

		interceptor := NewAuthInterceptor("initial")
		call := interceptor.WrapUnary(func(
			_ context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			header := req.Header().Get(types.AuthorizationKey)
			assert.True(t, tokens[strings.TrimPrefix(header, types.AuthSchemeBearer+" ")], header)
			return nil, nil
		})

		var wg sync.WaitGroup
		wg.Go(func() {
			for i := range 8 {
				interceptor.SetToken(fmt.Sprintf("token-%d", i))
			}
		})
		for range 4 {
			wg.Go(func() {
				for range 64 {
					_, _ = call(context.Background(), connect.NewRequest(&api.ActivateClientRequest{}))
				}
			})
		}
		wg.Wait()

		_, _ = call(context.Background(), connect.NewRequest(&api.ActivateClientRequest{}))
		assert.Equal(t, "token-7", interceptor.loadToken())
	})

	t.Run("zero value", func(t *testing.T) {
		interceptor := &AuthInterceptor{}
		var got string
		call := interceptor.WrapUnary(func(
			_ context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			got = req.Header().Get(types.AuthorizationKey)
			return nil, nil
		})
		_, err := call(context.Background(), connect.NewRequest(&api.ActivateClientRequest{}))
		assert.NoError(t, err)
		assert.Equal(t, types.AuthSchemeBearer+" ", got)
	})
}
