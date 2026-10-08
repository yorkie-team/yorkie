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
	"context"
	"fmt"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

const (
	userScopedProcedure    = "/yorkie.v1.AdminService/GetProject"
	projectScopedProcedure = "/yorkie.v1.AdminService/ListDocuments"
)

// adminProcedures returns every procedure of AdminService, read from the
// generated file descriptor so that a method added to the proto shows up here
// without the test being edited.
func adminProcedures(t *testing.T) []string {
	t.Helper()

	services := api.File_yorkie_v1_admin_proto.Services()
	for i := range services.Len() {
		service := services.Get(i)
		if string(service.FullName()) != "yorkie.v1.AdminService" {
			continue
		}

		var procedures []string
		methods := service.Methods()
		for j := range methods.Len() {
			procedures = append(procedures, fmt.Sprintf("/%s/%s", service.FullName(), methods.Get(j).Name()))
		}
		return procedures
	}

	t.Fatal("yorkie.v1.AdminService not found in the admin descriptor")
	return nil
}

func TestProjectScopedMethods(t *testing.T) {
	t.Run("every listed method exists test", func(t *testing.T) {
		procedures := make(map[string]struct{})
		for _, procedure := range adminProcedures(t) {
			procedures[procedure] = struct{}{}
		}

		// A typo in the list would silently make a project-scoped method
		// Bearer-only, which is exactly the mismatch this guards against.
		for procedure := range projectScopedMethods {
			_, ok := procedures[procedure]
			assert.True(t, ok, "%s is not a method of AdminService", procedure)
		}
	})

	t.Run("no method is both unauthenticated and project scoped test", func(t *testing.T) {
		for _, procedure := range adminProcedures(t) {
			if !isRequiredAuth(procedure) {
				assert.False(t, isProjectScoped(procedure), "%s needs no auth yet is project scoped", procedure)
			}
		}
	})
}

func TestAdminServiceAuthenticateScheme(t *testing.T) {
	// The mismatched scheme is rejected before the backend is read, so an
	// interceptor without one is enough to exercise these paths.
	interceptor := &AdminServiceInterceptor{}

	t.Run("api key on a user scoped method is rejected test", func(t *testing.T) {
		header := http.Header{}
		header.Set(types.AuthorizationKey, fmt.Sprintf("%s %s", types.AuthSchemeAPIKey, "secret-key"))

		_, err := interceptor.authenticate(context.Background(), userScopedProcedure, header)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.ErrorIs(t, err, ErrSchemeNotAllowed)
	})

	t.Run("bearer on a project scoped method is rejected test", func(t *testing.T) {
		header := http.Header{}
		header.Set(types.AuthorizationKey, fmt.Sprintf("%s %s", types.AuthSchemeBearer, "token"))

		_, err := interceptor.authenticate(context.Background(), projectScopedProcedure, header)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.ErrorIs(t, err, ErrSchemeNotAllowed)
	})

	t.Run("session cookie on a project scoped method is rejected test", func(t *testing.T) {
		header := http.Header{}
		header.Set("Cookie", fmt.Sprintf("%s=token", types.SessionKey))

		_, err := interceptor.authenticate(context.Background(), projectScopedProcedure, header)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.ErrorIs(t, err, ErrSchemeNotAllowed)
	})

	t.Run("missing authorization is still unauthenticated test", func(t *testing.T) {
		_, err := interceptor.authenticate(context.Background(), projectScopedProcedure, http.Header{})
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("unknown scheme is still unauthenticated test", func(t *testing.T) {
		header := http.Header{}
		header.Set(types.AuthorizationKey, "Basic dXNlcjpwYXNz")

		_, err := interceptor.authenticate(context.Background(), projectScopedProcedure, header)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
