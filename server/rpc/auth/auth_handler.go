/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"net/http"
	"strings"
	"time"

	"github.com/lithammer/shortuuid/v4"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/users"
)

// Config is the configuration for GitHub OAuth.
type Config struct {
	GitHubClientID            string `yaml:"GitHubClientID"`
	GitHubClientSecret        string `yaml:"GitHubClientSecret"`
	GitHubRedirectURL         string `yaml:"GitHubRedirectURL"`
	GitHubCallbackRedirectURL string `yaml:"GitHubCallbackRedirectURL"`

	GitHubAuthURL       string `yaml:"GitHubAuthURL"`
	GitHubTokenURL      string `yaml:"GitHubTokenURL"`
	GitHubDeviceAuthURL string `yaml:"GitHubDeviceAuthURL"`
	GitHubUserURL       string `yaml:"GitHubUserURL"`
}

// NewAuthHandler creates handlers for cookie-based session.
func NewAuthHandler(be *backend.Backend, tokenManager *TokenManager, conf Config) (string, http.Handler) {
	manager := &AuthManager{
		providers: map[string]Provider{
			"github": newGitHubProvider(conf),
		},
		loginRedirectURL: conf.GitHubCallbackRedirectURL,
		be:               be,
		tokenManager:     tokenManager,
		stateStore:       cmap.New[string, loginState](),
	}

	// TODO(hackerwins): Consider to use prefix `yorkie.v1.AuthService` for consistency with other handlers.
	return "/auth/", manager
}

// AuthManager provides handlers for login, logout, and me in cookie-based session.
type AuthManager struct {
	// providers maps a route, as in /auth/{route}/login, to its provider.
	providers map[string]Provider

	// loginRedirectURL is where the browser goes after a provider login.
	loginRedirectURL string

	be           *backend.Backend
	tokenManager *TokenManager
	stateStore   *cmap.Map[string, loginState]
}

// stateTTL is how long a login state stays usable after it is issued.
const stateTTL = 10 * time.Minute

// loginState is a state issued for a login through one provider route.
type loginState struct {
	route    string
	issuedAt time.Time
}

// ServeHTTP routes the requests under /auth/.
func (h *AuthManager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/auth/me":
		h.handleMe(r.Context(), w, r)
		return
	case "/auth/logout":
		h.handleLogout(r.Context(), w, r)
		return
	case "/auth/login":
		h.handleLogin(r.Context(), w, r)
		return
	}

	if route, provider, ok := h.providerOf(r.URL.Path, "/login"); ok {
		h.handleProviderLogin(w, r, route, provider)
		return
	}
	if route, provider, ok := h.providerOf(r.URL.Path, "/callback"); ok {
		h.handleProviderCallback(r.Context(), w, r, route, provider)
		return
	}
	http.NotFound(w, r)
}

// providerOf returns the route in the given path, which is
// /auth/{route}{suffix}, and the provider registered for it.
func (h *AuthManager) providerOf(path, suffix string) (string, Provider, bool) {
	route, ok := strings.CutPrefix(path, "/auth/")
	if !ok {
		return "", nil, false
	}
	route, ok = strings.CutSuffix(route, suffix)
	if !ok {
		return "", nil, false
	}

	provider, ok := h.providers[route]
	return route, provider, ok
}

func (h *AuthManager) handleMe(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(types.SessionKey)
	if err != nil || cookie.Value == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	claims, err := h.tokenManager.Verify(cookie.Value)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := users.GetUserByName(ctx, h.be, claims.Username)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		AuthProvider string `json:"authProvider"`
		Username     string `json:"username"`
	}

	body.AuthProvider = user.AuthProvider
	body.Username = user.Username

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *AuthManager) handleLogout(_ context.Context, w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   types.SessionKey,
		Value:  "",
		Path:   "/",
		MaxAge: 0,
	})
}

func (h *AuthManager) handleLogin(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	if body.Username == "" || body.Password == "" {
		http.Error(w, "Username and Password are required", http.StatusBadRequest)
		return
	}

	user, err := users.IsCorrectPassword(ctx, h.be, body.Username, body.Password)
	if err == database.ErrUserNotFound || err == database.ErrMismatchedPassword {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	token, err := h.tokenManager.Generate(user.Username)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.setCookie(w, token)
}

func (h *AuthManager) handleProviderLogin(
	w http.ResponseWriter,
	r *http.Request,
	route string,
	provider Provider,
) {
	state := shortuuid.New()
	h.stateStore.Set(state, loginState{route: route, issuedAt: time.Now()})
	h.cleanupOldStates()

	http.Redirect(w, r, provider.LoginURL(state), http.StatusTemporaryRedirect)
}

func (h *AuthManager) handleProviderCallback(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	route string,
	provider Provider,
) {
	stateConsumed := false
	identity, err := provider.Callback(ctx, r, func(state string) bool {
		stateConsumed = h.consumeState(state, route)
		return stateConsumed
	})
	if errors.Is(err, errInvalidState) {
		logging.DefaultLogger().Warnf("%s login: %v", provider.Name(), err)
		http.Error(w, errInvalidState.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		// Provider errors may carry details of the identity source, so they
		// go to the log instead of the browser.
		logging.DefaultLogger().Warnf("%s login: %v", provider.Name(), err)
		http.Error(w, "Failed to sign in", http.StatusInternalServerError)
		return
	}
	if !stateConsumed {
		// A provider that returns an identity without consuming a state
		// would accept a callback this server never started.
		http.Error(w, errInvalidState.Error(), http.StatusBadRequest)
		return
	}
	if identity == nil || identity.Subject == "" || identity.Username == "" {
		logging.DefaultLogger().Warnf("%s login: no subject or username in the identity", provider.Name())
		http.Error(w, "Failed to sign in", http.StatusInternalServerError)
		return
	}

	user, err := users.GetOrCreateUserByProvider(ctx, h.be, provider.Name(), identity.Subject, identity.Username)
	if errors.Is(err, database.ErrUserAuthProviderMismatch) {
		http.Error(w, "Username is already used by an account of another provider", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Failed to get or create user", http.StatusInternalServerError)
		return
	}

	token, err := h.tokenManager.Generate(user.Username)
	if err != nil {
		http.Error(w, "Failed to create token", http.StatusInternalServerError)
		return
	}

	h.setCookie(w, token)
	http.Redirect(w, r, h.loginRedirectURL, http.StatusTemporaryRedirect)
}

func (h *AuthManager) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     types.SessionKey,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   3600 * 24,
	})
}

// consumeState deletes the given state and reports whether it was issued for
// the given route and has not expired.
func (h *AuthManager) consumeState(state, route string) bool {
	valid := false
	h.stateStore.Delete(state, func(value loginState, exists bool) bool {
		valid = exists && value.route == route && time.Since(value.issuedAt) < stateTTL
		return exists
	})
	return valid
}

func (h *AuthManager) cleanupOldStates() {
	threshold := time.Now().Add(-stateTTL)

	for _, state := range h.stateStore.Keys() {
		h.stateStore.Delete(state, func(value loginState, exists bool) bool {
			return exists && value.issuedAt.Before(threshold)
		})
	}
}
