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
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/users"
)

const (
	userScopedProcedure     = "/yorkie.v1.AdminService/GetProject"
	projectScopedProcedure  = "/yorkie.v1.AdminService/ListDocuments"
	schemeAgnosticProcedure = "/yorkie.v1.AdminService/GetServerVersion"
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

	t.Run("every project scoped method requires a known role test", func(t *testing.T) {
		for procedure, role := range projectScopedMethods {
			assert.NoError(t, role.Validate(), "%s requires an unknown role", procedure)
		}
	})

	t.Run("no method is both unauthenticated and project scoped test", func(t *testing.T) {
		for _, procedure := range adminProcedures(t) {
			if !isRequiredAuth(procedure) {
				assert.False(t, isProjectScoped(procedure), "%s needs no auth yet is project scoped", procedure)
			}
		}
	})

	t.Run("every scheme agnostic method exists and reads no scope test", func(t *testing.T) {
		procedures := make(map[string]struct{})
		for _, procedure := range adminProcedures(t) {
			procedures[procedure] = struct{}{}
		}

		for procedure := range schemeAgnosticMethods {
			_, ok := procedures[procedure]
			assert.True(t, ok, "%s is not a method of AdminService", procedure)
			assert.False(t, isProjectScoped(procedure), "%s reads no scope yet is project scoped", procedure)
		}
	})
}

func TestIsSchemeAllowed(t *testing.T) {
	t.Run("scheme must match the scope the method reads test", func(t *testing.T) {
		assert.True(t, isSchemeAllowed(userScopedProcedure, types.AuthSchemeBearer, false))
		assert.False(t, isSchemeAllowed(userScopedProcedure, types.AuthSchemeAPIKey, false))
		assert.True(t, isSchemeAllowed(projectScopedProcedure, types.AuthSchemeAPIKey, false))
		assert.False(t, isSchemeAllowed(projectScopedProcedure, types.AuthSchemeBearer, false))
	})

	// A session token names its project in a header, so it populates the
	// project scope only when that header is present.
	t.Run("bearer reaches a project scoped method with a project name test", func(t *testing.T) {
		assert.True(t, isSchemeAllowed(projectScopedProcedure, types.AuthSchemeBearer, true))
		assert.True(t, isSchemeAllowed(userScopedProcedure, types.AuthSchemeBearer, true))
		assert.False(t, isSchemeAllowed(userScopedProcedure, types.AuthSchemeAPIKey, true))
	})

	// GetServerVersion reads neither scope and answered to both schemes before
	// the gate existed, so neither credential may be turned away here.
	t.Run("a method reading no scope keeps both schemes test", func(t *testing.T) {
		assert.True(t, isSchemeAllowed(schemeAgnosticProcedure, types.AuthSchemeAPIKey, false))
		assert.True(t, isSchemeAllowed(schemeAgnosticProcedure, types.AuthSchemeBearer, false))
	})

	t.Run("an unknown scheme is left to credential verification test", func(t *testing.T) {
		assert.True(t, isSchemeAllowed(userScopedProcedure, "Basic", false))
		assert.True(t, isSchemeAllowed(projectScopedProcedure, "Basic", false))
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

func TestAdminServiceAuthenticateAPIKey(t *testing.T) {
	ctx := context.Background()

	db, err := memory.New()
	assert.NoError(t, err)
	_, defaultProject, err := db.EnsureDefaultUserAndProject(ctx, "admin", "admin-password")
	assert.NoError(t, err)

	newInterceptor := func(useDefaultProject bool) *AdminServiceInterceptor {
		return &AdminServiceInterceptor{backend: &backend.Backend{
			Config: &backend.Config{UseDefaultProject: useDefaultProject},
			DB:     db,
		}}
	}
	apiKeyHeader := func(param string) http.Header {
		header := http.Header{}
		header.Set(types.AuthorizationKey, fmt.Sprintf("%s %s", types.AuthSchemeAPIKey, param))
		return header
	}

	t.Run("secret key authenticates as its project test", func(t *testing.T) {
		for _, useDefaultProject := range []bool{true, false} {
			interceptor := newInterceptor(useDefaultProject)

			ctx, err := interceptor.authenticate(ctx, projectScopedProcedure, apiKeyHeader(defaultProject.SecretKey))
			assert.NoError(t, err)
			assert.Equal(t, database.DefaultProjectID, projects.From(ctx).ID)
		}
	})

	// UseDefaultProject lets a client without a public key reach the default
	// project on YorkieService. It must not let an admin request without a
	// secret key act on that project, whatever the setting.
	t.Run("keyless api key is rejected even with the default project test", func(t *testing.T) {
		params := map[string]string{
			"empty":               "",
			"whitespace only":     " \t ",
			"non-breaking space":  "\u00a0",
			"mixed whitespace":    " \u00a0\t",
			"double non-breaking": "\u00a0\u00a0",
		}

		for _, useDefaultProject := range []bool{true, false} {
			interceptor := newInterceptor(useDefaultProject)
			for name, param := range params {
				_, err := interceptor.authenticate(ctx, projectScopedProcedure, apiKeyHeader(param))
				assert.Equal(
					t,
					connect.CodeUnauthenticated,
					connect.CodeOf(err),
					"%s key with UseDefaultProject=%v", name, useDefaultProject,
				)
				assert.ErrorIs(t, err, ErrSecretKeyNotProvided, "%s key with UseDefaultProject=%v", name, useDefaultProject)
			}
		}
	})
}

func TestAdminServiceAuthenticateBearerWithProject(t *testing.T) {
	ctx := context.Background()

	const (
		readProcedure  = "/yorkie.v1.AdminService/ListDocuments"
		writeProcedure = "/yorkie.v1.AdminService/RemoveDocumentByAdmin"
	)

	db, err := memory.New()
	assert.NoError(t, err)
	owner, _, err := db.EnsureDefaultUserAndProject(ctx, "owner", "owner-password")
	assert.NoError(t, err)
	project, err := db.CreateProjectInfo(ctx, t.Name(), owner.ID)
	assert.NoError(t, err)

	tokenManager := auth.NewTokenManager("token-secret", gotime.Hour)
	interceptor := &AdminServiceInterceptor{
		backend:      &backend.Backend{Config: &backend.Config{}, DB: db},
		tokenManager: tokenManager,
	}

	newUser := func(name string, role database.MemberRole) {
		info, err := db.CreateUserInfo(ctx, name, "password")
		assert.NoError(t, err)
		if role != "" {
			_, err = db.UpsertMemberInfo(ctx, project.ID, info.ID, owner.ID, role)
			assert.NoError(t, err)
		}
	}
	newUser("admin", database.Admin)
	newUser("member", database.Member)
	newUser("stranger", "")

	bearerHeader := func(username, projectName string) http.Header {
		token, err := tokenManager.Generate(username)
		assert.NoError(t, err)

		header := http.Header{}
		header.Set(types.AuthorizationKey, fmt.Sprintf("%s %s", types.AuthSchemeBearer, token))
		if projectName != "" {
			header.Set(types.ProjectNameKey, projectName)
		}
		return header
	}

	t.Run("role decides which project scoped methods a user reaches test", func(t *testing.T) {
		tests := []struct {
			username  string
			procedure string
			allowed   bool
		}{
			{"owner", readProcedure, true},
			{"owner", writeProcedure, true},
			{"admin", readProcedure, true},
			{"admin", writeProcedure, true},
			{"member", readProcedure, true},
			{"member", writeProcedure, false},
			{"stranger", readProcedure, false},
			{"stranger", writeProcedure, false},
		}

		for _, tt := range tests {
			ctx, err := interceptor.authenticate(ctx, tt.procedure, bearerHeader(tt.username, project.Name))
			if !tt.allowed {
				assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "%s on %s", tt.username, tt.procedure)
				assert.ErrorIs(t, err, ErrProjectAccessDenied, "%s on %s", tt.username, tt.procedure)
				continue
			}

			assert.NoError(t, err, "%s on %s", tt.username, tt.procedure)
			assert.Equal(t, project.ID, projects.From(ctx).ID)
			assert.Equal(t, tt.username, users.From(ctx).Username)
		}
	})

	// A member reaches the project through its session, never through the
	// key, so the project it is handed must not carry the key either.
	t.Run("member context carries no secret key test", func(t *testing.T) {
		ctx, err := interceptor.authenticate(ctx, readProcedure, bearerHeader("member", project.Name))
		assert.NoError(t, err)
		assert.Empty(t, projects.From(ctx).SecretKey)
	})

	t.Run("unknown project answers like a forbidden one test", func(t *testing.T) {
		_, err := interceptor.authenticate(ctx, readProcedure, bearerHeader("owner", "no-such-project"))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.ErrorIs(t, err, ErrProjectAccessDenied)
	})

	t.Run("bearer without a project name is still rejected test", func(t *testing.T) {
		_, err := interceptor.authenticate(ctx, readProcedure, bearerHeader("owner", ""))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.ErrorIs(t, err, ErrSchemeNotAllowed)
	})

	t.Run("invalid token with a project name is unauthenticated test", func(t *testing.T) {
		header := http.Header{}
		header.Set(types.AuthorizationKey, fmt.Sprintf("%s %s", types.AuthSchemeBearer, "not-a-token"))
		header.Set(types.ProjectNameKey, project.Name)

		_, err := interceptor.authenticate(ctx, readProcedure, header)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}
