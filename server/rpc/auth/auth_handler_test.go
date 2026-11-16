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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
)

// newGitHubStub serves the token and user endpoints of a GitHub OAuth app.
// The user endpoint answers with the given login.
func newGitHubStub(t *testing.T, login string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
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
	db, err := memory.New()
	require.NoError(t, err)
	be := &backend.Backend{DB: db}

	github := newGitHubStub(t, login)
	_, handler := NewAuthHandler(be, NewTokenManager("test-secret-key", time.Hour), Config{
		GitHubClientID:            "client-id",
		GitHubClientSecret:        "client-secret",
		GitHubRedirectURL:         "http://localhost/auth/github/callback",
		GitHubCallbackRedirectURL: "http://localhost/dashboard",
		GitHubAuthURL:             github.URL + "/authorize",
		GitHubTokenURL:            github.URL + "/token",
		GitHubUserURL:             github.URL + "/user",
	})
	return handler, be
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
