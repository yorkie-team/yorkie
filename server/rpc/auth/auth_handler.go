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
	"fmt"
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

// Config is the configuration for dashboard login.
type Config struct {
	GitHubClientID            string `yaml:"GitHubClientID"`
	GitHubClientSecret        string `yaml:"GitHubClientSecret"`
	GitHubRedirectURL         string `yaml:"GitHubRedirectURL"`
	GitHubCallbackRedirectURL string `yaml:"GitHubCallbackRedirectURL"`

	GitHubAuthURL       string `yaml:"GitHubAuthURL"`
	GitHubTokenURL      string `yaml:"GitHubTokenURL"`
	GitHubDeviceAuthURL string `yaml:"GitHubDeviceAuthURL"`
	GitHubUserURL       string `yaml:"GitHubUserURL"`

	// ExternalProviders are login brokers run outside Yorkie. After a login
	// through any provider, the browser goes to GitHubCallbackRedirectURL.
	ExternalProviders []ExternalProviderConfig `yaml:"ExternalProviders"`
}

// Validate validates the external providers.
func (c *Config) Validate() error {
	names := make(map[string]bool, len(c.ExternalProviders))
	for i := range c.ExternalProviders {
		provider := &c.ExternalProviders[i]
		if err := provider.Validate(); err != nil {
			return err
		}
		if names[provider.Name] {
			return fmt.Errorf("%s: duplicate name: %w", provider.Name, ErrInvalidExternalProvider)
		}
		names[provider.Name] = true
	}
	return nil
}

// NewAuthHandler creates handlers for cookie-based session.
func NewAuthHandler(be *backend.Backend, tokenManager *TokenManager, conf Config) (string, http.Handler, error) {
	if err := conf.Validate(); err != nil {
		return "", nil, err
	}

	manager := &AuthManager{
		providers: map[string]Provider{
			"github": newGitHubProvider(conf),
		},
		loginRedirectURL: conf.GitHubCallbackRedirectURL,
		be:               be,
		tokenManager:     tokenManager,
		stateStore:       cmap.New[string, loginState](),
	}

	// GitHub routes answer whether configured or not, as they always have;
	// only a configured GitHub app is offered on the login page.
	if conf.GitHubClientID != "" {
		manager.listed = append(manager.listed, listedProvider{
			Name:        "github",
			DisplayName: "GitHub",
			LoginPath:   "/auth/github/login",
		})
	}
	for _, providerConf := range conf.ExternalProviders {
		provider, err := newExternalProvider(providerConf)
		if err != nil {
			return "", nil, err
		}
		route := "external/" + providerConf.Name
		manager.providers[route] = provider
		manager.listed = append(manager.listed, listedProvider{
			Name:        providerConf.Name,
			DisplayName: providerConf.DisplayName,
			LoginPath:   "/auth/" + route + "/login",
		})
	}

	// TODO(hackerwins): Consider to use prefix `yorkie.v1.AuthService` for consistency with other handlers.
	return "/auth/", manager, nil
}

// listedProvider is a login method offered on the dashboard login page.
type listedProvider struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	LoginPath   string `json:"loginPath"`
}

// AuthManager provides handlers for login, logout, and me in cookie-based session.
type AuthManager struct {
	// providers maps a route, as in /auth/{route}/login, to its provider.
	providers map[string]Provider

	// listed are the providers offered on the dashboard login page, in the
	// order to show them.
	listed []listedProvider

	// loginRedirectURL is where the browser goes after a provider login.
	loginRedirectURL string

	be           *backend.Backend
	tokenManager *TokenManager
	stateStore   *cmap.Map[string, loginState]
}

const (
	// stateTTL is how long a login state stays usable after it is issued.
	stateTTL = 10 * time.Minute

	// stateCookieName is the cookie that ties a login state to the browser
	// that started the login. The __Host- prefix makes browsers refuse the
	// cookie unless it is Secure, host-only and on Path=/, so a sibling
	// subdomain cannot plant one.
	stateCookieName = "__Host-yorkie-auth-state"

	// maxPendingLogins caps the states waiting for a callback, since anyone
	// can start a login.
	maxPendingLogins = 10000
)

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
	case "/auth/providers":
		h.handleProviders(w, r)
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
	h.cleanupOldStates()
	if h.stateStore.Len() >= maxPendingLogins {
		http.Error(w, "Too many logins in progress", http.StatusServiceUnavailable)
		return
	}

	state := shortuuid.New()
	h.stateStore.Set(state, loginState{route: route, issuedAt: time.Now()})
	http.SetCookie(w, stateCookie(state, int(stateTTL/time.Second)))
	http.Redirect(w, r, provider.LoginURL(state), http.StatusTemporaryRedirect)
}

func (h *AuthManager) handleProviderCallback(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	route string,
	provider Provider,
) {
	browserState := ""
	if cookie, err := r.Cookie(stateCookieName); err == nil {
		browserState = cookie.Value
	}
	http.SetCookie(w, stateCookie("", -1))

	stateConsumed := false
	identity, err := provider.Callback(ctx, r, func(state string) bool {
		// A state from a login another browser started is refused, so a
		// forwarded callback cannot sign this browser in.
		stateConsumed = h.consumeState(state, route) && state == browserState
		return stateConsumed
	})
	if errors.Is(err, errInvalidState) {
		logging.DefaultLogger().Warnf("%s login: %v", provider.Name(), err)
		http.Error(w, errInvalidState.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, errLoginRejected) {
		logging.DefaultLogger().Warnf("%s login: %v", provider.Name(), err)
		http.Error(w, "Failed to sign in", http.StatusUnauthorized)
		return
	}
	if errors.Is(err, errInvalidUsername) {
		logging.DefaultLogger().Warnf("%s login: %v", provider.Name(), err)
		http.Error(w, "The username from the provider is not a valid username", http.StatusBadRequest)
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
		http.Error(w, "Username is already used by another account", http.StatusConflict)
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
	// 303 so that the browser follows with a GET: a 307 would post the
	// assertion of a broker callback on to the dashboard.
	http.Redirect(w, r, h.loginRedirectURL, http.StatusSeeOther)
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

// stateCookie returns the state cookie with the given value and max age.
// SameSite=None because a broker posts the callback from its own site.
func stateCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     stateCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   maxAge,
	}
}

// handleProviders answers the login methods the dashboard should offer.
func (h *AuthManager) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body := struct {
		PasswordLogin bool             `json:"passwordLogin"`
		Providers     []listedProvider `json:"providers"`
	}{
		PasswordLogin: true,
		Providers:     h.listed,
	}
	if body.Providers == nil {
		body.Providers = []listedProvider{}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
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
