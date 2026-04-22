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

package interceptors

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestClusterServiceAuthenticate(t *testing.T) {
	t.Run("valid secret passes", func(t *testing.T) {
		interceptor := &ClusterServiceInterceptor{
			clusterSecret: "test-secret",
		}

		header := http.Header{}
		header.Set(clusterSecretHeader, "test-secret")

		err := interceptor.authenticate(header)
		assert.NoError(t, err)
	})

	t.Run("invalid secret is rejected", func(t *testing.T) {
		interceptor := &ClusterServiceInterceptor{
			clusterSecret: "test-secret",
		}

		header := http.Header{}
		header.Set(clusterSecretHeader, "wrong-secret")

		err := interceptor.authenticate(header)
		assert.Error(t, err)

		var connectErr *connect.Error
		assert.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
		assert.Contains(t, connectErr.Message(), "invalid cluster secret")
	})

	t.Run("missing secret header is rejected", func(t *testing.T) {
		interceptor := &ClusterServiceInterceptor{
			clusterSecret: "test-secret",
		}

		header := http.Header{}

		err := interceptor.authenticate(header)
		assert.Error(t, err)

		var connectErr *connect.Error
		assert.ErrorAs(t, err, &connectErr)
		assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
	})

	t.Run("empty cluster secret allows all requests", func(t *testing.T) {
		interceptor := &ClusterServiceInterceptor{
			clusterSecret: "",
		}

		header := http.Header{}
		header.Set(clusterSecretHeader, "any-secret")

		err := interceptor.authenticate(header)
		assert.NoError(t, err)
	})

	t.Run("empty cluster secret allows requests without header", func(t *testing.T) {
		interceptor := &ClusterServiceInterceptor{
			clusterSecret: "",
		}

		header := http.Header{}

		err := interceptor.authenticate(header)
		assert.NoError(t, err)
	})
}
