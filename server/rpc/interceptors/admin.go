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

package interceptors

import (
	"context"
	goerrors "errors"
	"fmt"
	"net/http"
	"strings"
	gotime "time"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/server/users"
)

var (
	// ErrUnauthenticated is returned when authentication is failed.
	ErrUnauthenticated = errors.Unauthenticated("authorization is not provided")

	// ErrInvalidAuthHeaderFormat is returned when the authorization header format is invalid.
	ErrInvalidAuthHeaderFormat = errors.InvalidArgument("invalid authorization header format")

	// ErrSecretKeyNotProvided is returned when the secret key is not provided.
	ErrSecretKeyNotProvided = errors.Unauthenticated("secret key is not provided")

	// ErrSchemeNotAllowed is returned when the authorization scheme is not the
	// one the requested method expects.
	ErrSchemeNotAllowed = errors.PermissionDenied("authorization scheme is not allowed for this method")

	// ErrProjectAccessDenied is returned when a session-authenticated user is
	// not allowed to call a project-scoped method on the named project.
	ErrProjectAccessDenied = errors.PermissionDenied("project access is not allowed for this method")
)

// projectScopedMethods maps each AdminService method that reads the project
// from the context with projects.From to the least project role a user needs
// to call it with a session token. Every other authenticated method is
// user-scoped and takes a session token alone.
//
// A project-scoped method is reached in one of two ways. A project secret key
// (API-Key) carries the whole authority of the project, so the role below does
// not apply to it. A session token (Bearer) together with the project named in
// the ProjectNameKey header is checked against the caller's role in that
// project, so a member can read the project without being handed a key that
// would let it act as an owner.
//
// NOTE(hackerwins): The set lists the project-scoped side on purpose. A method
// added later is user-scoped until it is listed here, so it cannot silently
// inherit a scheme it does not read. Methods that change the project's
// documents, schemas or sessions require Admin; only reads are open to Member.
var projectScopedMethods = map[string]database.MemberRole{
	"/yorkie.v1.AdminService/GetProjectStats":        database.Member,
	"/yorkie.v1.AdminService/CreateDocument":         database.Admin,
	"/yorkie.v1.AdminService/ListDocuments":          database.Member,
	"/yorkie.v1.AdminService/GetDocument":            database.Member,
	"/yorkie.v1.AdminService/GetDocuments":           database.Member,
	"/yorkie.v1.AdminService/SearchDocuments":        database.Member,
	"/yorkie.v1.AdminService/UpdateDocument":         database.Admin,
	"/yorkie.v1.AdminService/RemoveDocumentByAdmin":  database.Admin,
	"/yorkie.v1.AdminService/CompactDocumentByAdmin": database.Admin,
	"/yorkie.v1.AdminService/GetSnapshotMeta":        database.Member,
	"/yorkie.v1.AdminService/ListChanges":            database.Member,
	"/yorkie.v1.AdminService/CreateSchema":           database.Admin,
	"/yorkie.v1.AdminService/ListSchemas":            database.Member,
	"/yorkie.v1.AdminService/GetSchema":              database.Member,
	"/yorkie.v1.AdminService/GetSchemas":             database.Member,
	"/yorkie.v1.AdminService/RemoveSchema":           database.Admin,
	"/yorkie.v1.AdminService/ListChannels":           database.Member,
	"/yorkie.v1.AdminService/GetChannels":            database.Member,
	"/yorkie.v1.AdminService/BroadcastByAdmin":       database.Admin,
	"/yorkie.v1.AdminService/RevalidateAccess":       database.Admin,
}

// secretKeyRole is the project role a secret key stands in for. Only Admin and
// above may read the key, so it carries no more than Admin's authority, and the
// per-method table applies to it as it does to a session of that role.
const secretKeyRole = database.Admin

// isSecretKeyAllowed reports whether a project secret key may call the given
// project-scoped method. The key names no user, so its role is secretKeyRole.
func isSecretKeyAllowed(method string) bool {
	required, ok := projectScopedMethods[method]
	return !ok || secretKeyRole.IsAtLeast(required)
}

// schemeAgnosticMethods is the set of authenticated AdminService methods that
// read neither scope. They answer the same way whichever credential is
// presented, so both schemes stay valid for them and only the credential
// itself is verified.
var schemeAgnosticMethods = map[string]struct{}{
	"/yorkie.v1.AdminService/GetServerVersion": {},
}

func isAdminService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.AdminService")
}

func isRequiredAuth(method string) bool {
	return method != "/yorkie.v1.AdminService/LogIn" &&
		method != "/yorkie.v1.AdminService/SignUp" &&
		method != "/yorkie.v1.AdminService/ChangePassword" &&
		method != "/yorkie.v1.AdminService/DeleteAccount"
}

// isProjectScoped reports whether the given method is authenticated by a
// project secret key instead of a session token.
func isProjectScoped(method string) bool {
	_, ok := projectScopedMethods[method]
	return ok
}

// isSchemeAgnostic reports whether the given method reads neither scope and so
// accepts either scheme.
func isSchemeAgnostic(method string) bool {
	_, ok := schemeAgnosticMethods[method]
	return ok
}

// isSchemeAllowed reports whether the given scheme populates the scope the
// given method reads. A Bearer token populates the project scope only together
// with a project name, so hasProjectName says whether one was sent. A scheme
// this does not recognize is left to the credential verification below, which
// rejects it as unauthenticated.
func isSchemeAllowed(method, scheme string, hasProjectName bool) bool {
	if isSchemeAgnostic(method) {
		return true
	}

	if strings.EqualFold(scheme, types.AuthSchemeAPIKey) {
		return isProjectScoped(method)
	}
	if strings.EqualFold(scheme, types.AuthSchemeBearer) {
		return !isProjectScoped(method) || hasProjectName
	}

	return true
}

// AdminServiceInterceptor is an interceptor for building additional context
// and handling authentication for AdminService.
type AdminServiceInterceptor struct {
	backend      *backend.Backend
	requestID    *requestID
	tokenManager *auth.TokenManager
}

// NewAdminServiceInterceptor creates a new instance of AdminServiceInterceptor.
func NewAdminServiceInterceptor(be *backend.Backend, tokenManager *auth.TokenManager) *AdminServiceInterceptor {
	return &AdminServiceInterceptor{
		backend:      be,
		requestID:    newRequestID("a"),
		tokenManager: tokenManager,
	}
}

// WrapUnary creates a unary server interceptor for authentication.
func (i *AdminServiceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if !isAdminService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		start := gotime.Now()
		ctx, err := i.buildContext(ctx, req.Spec().Procedure, req.Header())
		if err != nil {
			return nil, err
		}

		res, err := next(ctx, req)

		// TODO(hackerwins, emplam27): Consider splitting between admin and sdk metrics.
		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(req.Header())
		i.backend.Metrics.AddUserAgentWithEmptyProject(
			i.backend.Config.Hostname,
			sdkType,
			sdkVersion,
			req.Spec().Procedure,
		)

		if split := strings.Split(req.Spec().Procedure, "/"); len(split) == 3 {
			code := connecthelper.CodeOf(err)
			i.backend.Metrics.AddServerHandledCounter("unary", split[1], split[2], code, i.backend.Config.Hostname)
			i.backend.Metrics.ObserveServerHandledResponseSeconds(
				"unary",
				split[1],
				split[2],
				code,
				i.backend.Config.Hostname,
				gotime.Since(start).Seconds(),
			)
		}

		return res, err
	}
}

// WrapStreamingClient creates a stream client interceptor for authentication.
func (i *AdminServiceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for authentication.
func (i *AdminServiceInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isAdminService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		ctx, err := i.buildContext(ctx, conn.Spec().Procedure, conn.RequestHeader())
		if err != nil {
			return err
		}

		err = next(ctx, conn)

		// TODO(hackerwins, emplam27): Consider splitting between admin and sdk metrics.
		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(conn.RequestHeader())
		i.backend.Metrics.AddUserAgentWithEmptyProject(
			i.backend.Config.Hostname,
			sdkType,
			sdkVersion,
			conn.Spec().Procedure,
		)

		if split := strings.Split(conn.Spec().Procedure, "/"); len(split) == 3 {
			i.backend.Metrics.AddServerHandledCounter(
				"server_stream",
				split[1],
				split[2],
				connecthelper.CodeOf(err),
				i.backend.Config.Hostname,
			)
		}

		return err
	}
}

// buildContext builds a new context with the given request header.
func (i *AdminServiceInterceptor) buildContext(
	ctx context.Context,
	procedure string,
	header http.Header,
) (context.Context, error) {
	if isRequiredAuth(procedure) {
		newContext, err := i.authenticate(ctx, procedure, header)
		if err != nil {
			return nil, err
		}
		ctx = newContext
	}

	ctx = logging.With(ctx, logging.NewNamed(i.requestID.next()))

	return ctx, nil
}

// authenticate does authenticate the request.
func (i *AdminServiceInterceptor) authenticate(
	ctx context.Context,
	procedure string,
	header http.Header,
) (context.Context, error) {
	// NOTE(hackerwins): The token can be provided by the Authorization header or cookie.
	authHeader := header.Get(types.AuthorizationKey)
	if authHeader == "" {
		cookie, err := (&http.Request{Header: header}).Cookie(types.SessionKey)
		if err != nil && err != http.ErrNoCookie {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if cookie != nil {
			authHeader = fmt.Sprintf("%s %s", types.AuthSchemeBearer, cookie.Value)
		}
	}
	if authHeader == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthenticated)
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrInvalidAuthHeaderFormat)
	}
	scheme := parts[0]
	param := parts[1]
	projectName := strings.TrimSpace(header.Get(types.ProjectNameKey))

	// NOTE(hackerwins): Each scheme populates only one of the two scopes:
	// Bearer puts the user into the context, API-Key puts the project. A method
	// reads one of them, so the scheme that does not match the method is
	// rejected here, before the handler reads a scope that was never set. A
	// method that reads neither scope keeps accepting both schemes. A Bearer
	// token reaches a project-scoped method only with a project name, and the
	// user's role in that project is checked below.
	if !isSchemeAllowed(procedure, scheme, projectName != "") {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrSchemeNotAllowed)
	}

	switch {
	case strings.EqualFold(scheme, types.AuthSchemeBearer):
		// If the scheme is Bearer, verify the token and retrieve the user.
		claims, err := i.tokenManager.Verify(param)
		if err == nil {
			user, err := users.GetUserByName(ctx, i.backend, claims.Username)
			if err == nil {
				ctx = users.With(ctx, user)
				if isProjectScoped(procedure) {
					return i.authorizeProject(ctx, procedure, user.ID, projectName)
				}
				return ctx, nil
			}
		}
	case strings.EqualFold(scheme, types.AuthSchemeAPIKey):
		// If the scheme is API-Key, verify the secret key and retrieve the project.
		//
		// NOTE(hackerwins): An empty key is rejected whatever UseDefaultProject
		// says. That setting lets a client without a public key reach the
		// default project on YorkieService; on AdminService an empty key used
		// to fall back to the default project, granting its admin methods to
		// a caller with no credential at all.
		secretKey := strings.TrimSpace(param)
		if secretKey == "" {
			return nil, connect.NewError(connect.CodeUnauthenticated, ErrSecretKeyNotProvided)
		}
		project, err := projects.ProjectFromSecretKey(ctx, i.backend, secretKey)
		if err == nil {
			if !isSecretKeyAllowed(procedure) {
				return nil, connect.NewError(connect.CodePermissionDenied, ErrProjectAccessDenied)
			}
			ctx = projects.With(ctx, project)
			return ctx, nil
		}
	}

	return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthenticated)
}

// authorizeProject puts the named project into the context of a
// session-authenticated request when the user's role in that project is at
// least the one the method requires.
//
// NOTE(hackerwins): A project the user cannot see and one that does not exist
// answer the same way, so the header cannot be used to probe project names.
// The role is validated before the comparison because MemberRole.IsAtLeast
// ranks a role it does not recognize above Owner; an unknown role fails closed.
func (i *AdminServiceInterceptor) authorizeProject(
	ctx context.Context,
	procedure string,
	userID types.ID,
	projectName string,
) (context.Context, error) {
	project, role, err := projects.ProjectAndRole(ctx, i.backend, userID, projectName)
	if err != nil {
		if goerrors.Is(err, database.ErrProjectNotFound) || goerrors.Is(err, database.ErrMemberNotFound) {
			return nil, connect.NewError(connect.CodePermissionDenied, ErrProjectAccessDenied)
		}
		return nil, err
	}

	required := projectScopedMethods[procedure]
	if role.Validate() != nil || !role.IsAtLeast(required) {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrProjectAccessDenied)
	}

	return projects.With(ctx, project), nil
}
