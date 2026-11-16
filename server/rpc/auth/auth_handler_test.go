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

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
)

// newGitHubStub serves the token and user endpoints of a GitHub OAuth app.
// The user endpoint answers with the given login. tokenRequests counts the
// code exchanges.
func newGitHubStub(t *testing.T, login string, tokenRequests *atomic.Int32) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		tokenRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "github-access-token",
			"token_type":   "bearer",
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"login": login})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// signInWithGitHub runs the login and callback steps of the GitHub flow
// against the handler and returns the callback response.
func signInWithGitHub(t *testing.T, handler http.Handler) *http.Response {
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/github/login", nil))
	require.Equal(t, http.StatusTemporaryRedirect, login.Code)

	location, err := url.Parse(login.Header().Get("Location"))
	require.NoError(t, err)
	state := location.Query().Get("state")
	require.NotEmpty(t, state)

	callback := httptest.NewRecorder()
	handler.ServeHTTP(callback, httptest.NewRequest(
		http.MethodGet,
		"/auth/github/callback?code=code&state="+url.QueryEscape(state),
		nil,
	))
	return callback.Result()
}

// newGitHubAuthHandler returns an auth handler on a memory backend whose
// GitHub endpoints answer with the given login.
func newGitHubAuthHandler(t *testing.T, login string) (http.Handler, *backend.Backend) {
	handler, be, _ := newCountingGitHubAuthHandler(t, login)
	return handler, be
}

// newCountingGitHubAuthHandler is newGitHubAuthHandler that also returns the
// number of code exchanges the GitHub stub served.
func newCountingGitHubAuthHandler(t *testing.T, login string) (http.Handler, *backend.Backend, *atomic.Int32) {
	db, err := memory.New()
	require.NoError(t, err)
	be := &backend.Backend{DB: db}

	tokenRequests := &atomic.Int32{}
	github := newGitHubStub(t, login, tokenRequests)
	_, handler := NewAuthHandler(be, NewTokenManager("test-secret-key", time.Hour), Config{
		GitHubClientID:            "client-id",
		GitHubClientSecret:        "client-secret",
		GitHubRedirectURL:         "http://localhost/auth/github/callback",
		GitHubCallbackRedirectURL: "http://localhost/dashboard",
		GitHubAuthURL:             github.URL + "/authorize",
		GitHubTokenURL:            github.URL + "/token",
		GitHubUserURL:             github.URL + "/user",
	})
	return handler, be, tokenRequests
}

func TestGitHubCallback(t *testing.T) {
	t.Run("signs in a new GitHub user", func(t *testing.T) {
		handler, _ := newGitHubAuthHandler(t, "octocat")

		resp := signInWithGitHub(t, handler)
		assert.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)

		var session *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == types.SessionKey {
				session = c
			}
		}
		require.NotNil(t, session)
		assert.NotEmpty(t, session.Value)

		again := signInWithGitHub(t, handler)
		assert.Equal(t, http.StatusTemporaryRedirect, again.StatusCode, "a returning GitHub user was refused")
	})

	t.Run("rejects a login whose name a local user holds", func(t *testing.T) {
		handler, be := newGitHubAuthHandler(t, "octo-local")
		_, err := be.DB.CreateUserInfo(t.Context(), "octo-local", "hashed-password")
		require.NoError(t, err)

		resp := signInWithGitHub(t, handler)
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
		for _, c := range resp.Cookies() {
			assert.NotEqual(t, types.SessionKey, c.Name, "issued a session cookie")
		}
	})
}

// hasSessionCookie reports whether the response sets the session cookie.
func hasSessionCookie(resp *http.Response) bool {
	for _, c := range resp.Cookies() {
		if c.Name == types.SessionKey && c.Value != "" {
			return true
		}
	}
	return false
}

func TestProviderRoutes(t *testing.T) {
	t.Run("rejects a callback whose state was not issued", func(t *testing.T) {
		handler, _, tokenRequests := newCountingGitHubAuthHandler(t, "octocat")

		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(
			http.MethodGet,
			"/auth/github/callback?code=code&state=forged",
			nil,
		))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.False(t, hasSessionCookie(rec.Result()))
		assert.Zero(t, tokenRequests.Load(), "exchanged the code before checking the state")
	})

	t.Run("rejects a state used twice", func(t *testing.T) {
		handler, _ := newGitHubAuthHandler(t, "octocat")

		login := httptest.NewRecorder()
		handler.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/github/login", nil))
		location, err := url.Parse(login.Header().Get("Location"))
		require.NoError(t, err)
		callbackURL := "/auth/github/callback?code=code&state=" + url.QueryEscape(location.Query().Get("state"))

		first := httptest.NewRecorder()
		handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, callbackURL, nil))
		require.Equal(t, http.StatusTemporaryRedirect, first.Code)

		second := httptest.NewRecorder()
		handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, callbackURL, nil))
		assert.Equal(t, http.StatusBadRequest, second.Code)
	})

	t.Run("answers not found for an unknown provider", func(t *testing.T) {
		handler, _ := newGitHubAuthHandler(t, "octocat")

		for _, path := range []string{"/auth/unknown/login", "/auth/unknown/callback", "/auth//login"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			assert.Equal(t, http.StatusNotFound, rec.Code, path)
		}
	})

	t.Run("refuses an identity from a provider that skipped the state", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{
			skipState: true,
			identity:  &Identity{Username: "careless-user"},
		})

		resp := signInWithFake(t, manager, "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.False(t, hasSessionCookie(resp))
	})

	t.Run("rejects a state issued for another provider", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{identity: &Identity{Username: "fake-user"}})
		manager.providers["other"] = &fakeProvider{identity: &Identity{Username: "other-user"}}

		login := httptest.NewRecorder()
		manager.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/other/login", nil))
		location, err := url.Parse(login.Header().Get("Location"))
		require.NoError(t, err)

		resp := signInWithFake(t, manager, location.Query().Get("state"))
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.False(t, hasSessionCookie(resp))
	})

	t.Run("rejects an expired state", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{identity: &Identity{Username: "fake-user"}})
		manager.stateStore.Set("stale", loginState{route: "fake", issuedAt: time.Now().Add(-stateTTL)})

		callback := httptest.NewRecorder()
		manager.ServeHTTP(callback, httptest.NewRequest(http.MethodGet, "/auth/fake/callback?state=stale", nil))
		assert.Equal(t, http.StatusBadRequest, callback.Code)
	})

	t.Run("keeps details of a state error out of the response", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{
			err: fmt.Errorf("%w: key secret-kid unknown", errInvalidState),
		})

		resp := signInWithFake(t, manager, "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "secret-kid")
	})

	t.Run("keeps provider error details out of the response", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{
			err: errors.New("identity source at 10.0.0.1 refused the client"),
		})

		resp := signInWithFake(t, manager, "")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "10.0.0.1")
		assert.False(t, hasSessionCookie(resp))
	})

	t.Run("refuses an identity without a username", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{identity: &Identity{}})

		resp := signInWithFake(t, manager, "")
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.False(t, hasSessionCookie(resp))
	})

	t.Run("signs a user of a registered provider in", func(t *testing.T) {
		manager := newFakeProviderManager(t, &fakeProvider{identity: &Identity{Username: "fake-user"}})

		resp := signInWithFake(t, manager, "")
		assert.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
		assert.True(t, hasSessionCookie(resp))

		user, err := manager.be.DB.FindUserInfoByName(t.Context(), "fake-user")
		require.NoError(t, err)
		assert.Equal(t, "fake", user.AuthProvider)
	})
}

// fakeProvider is a provider whose callback answers with the given identity
// or error. It reads the state from the query like GitHub.
type fakeProvider struct {
	identity  *Identity
	err       error
	skipState bool
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) LoginURL(state string) string {
	return "http://localhost/fake-login?state=" + url.QueryEscape(state)
}

func (p *fakeProvider) Callback(
	_ context.Context,
	r *http.Request,
	consumeState func(string) bool,
) (*Identity, error) {
	if !p.skipState && !consumeState(r.URL.Query().Get("state")) {
		return nil, errInvalidState
	}
	return p.identity, p.err
}

// newFakeProviderManager returns an auth manager on a memory backend with the
// given provider registered as the "fake" route.
func newFakeProviderManager(t *testing.T, provider Provider) *AuthManager {
	db, err := memory.New()
	require.NoError(t, err)
	return &AuthManager{
		providers:        map[string]Provider{"fake": provider},
		loginRedirectURL: "http://localhost/dashboard",
		be:               &backend.Backend{DB: db},
		tokenManager:     NewTokenManager("test-secret-key", time.Hour),
		stateStore:       cmap.New[string, loginState](),
	}
}

// signInWithFake starts a login on the fake route and sends its callback. An
// empty state means the state the login issued.
func signInWithFake(t *testing.T, manager *AuthManager, state string) *http.Response {
	login := httptest.NewRecorder()
	manager.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/fake/login", nil))
	require.Equal(t, http.StatusTemporaryRedirect, login.Code)

	if state == "" {
		location, err := url.Parse(login.Header().Get("Location"))
		require.NoError(t, err)
		state = location.Query().Get("state")
	}

	callback := httptest.NewRecorder()
	manager.ServeHTTP(callback, httptest.NewRequest(
		http.MethodGet,
		"/auth/fake/callback?state="+url.QueryEscape(state),
		nil,
	))
	return callback.Result()
}
