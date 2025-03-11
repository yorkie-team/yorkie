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
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/users"
)

// Config is the configuration for GitHub OAuth.
type Config struct {
	ClientID     string `yaml:"ClientID"`
	ClientSecret string `yaml:"ClientSecret"`
	RedirectURL  string `yaml:"RedirectURL"`
}

// NewAuthHandler creates handlers for cookie-based session.
func NewAuthHandler(be *backend.Backend, tokenManager *TokenManager, conf Config) (string, http.Handler) {
	oauthConf := &oauth2.Config{
		ClientID:     conf.ClientID,
		ClientSecret: conf.ClientSecret,
		RedirectURL:  conf.RedirectURL,
		Scopes:       []string{"user:email"},
		Endpoint:     github.Endpoint,
	}

	manager := &AuthManager{
		conf:         oauthConf,
		be:           be,
		tokenManager: tokenManager,
		stateString:  "random-state-string",
	}

	return "/auth/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/me":
			manager.handleMe(r.Context(), w, r)
		case "/auth/logout":
			manager.handleLogout(r.Context(), w, r)
		case "/auth/login":
			manager.handleLogin(r.Context(), w, r)
		case "/auth/github/login":
			manager.handleGitHubLogin(r.Context(), w, r)
		case "/auth/github/callback":
			manager.handleGitHubCallback(r.Context(), w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// AuthManager provides handlers for login, logout, and me in cookie-based session.
type AuthManager struct {
	conf         *oauth2.Config
	be           *backend.Backend
	tokenManager *TokenManager
	stateString  string
}

func (h *AuthManager) handleMe(_ context.Context, w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(types.SessionKey)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	claims, err := h.tokenManager.Verify(cookie.Value)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var body struct {
		Username string `json:"username"`
	}
	body.Username = claims.Username
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *AuthManager) handleLogout(_ context.Context, w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:  types.SessionKey,
		Value: "",
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

func (h *AuthManager) handleGitHubLogin(_ context.Context, w http.ResponseWriter, r *http.Request) {
	url := h.conf.AuthCodeURL(h.stateString)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func (h *AuthManager) handleGitHubCallback(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != h.stateString {
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	oauthToken, err := h.conf.Exchange(ctx, code)
	if err != nil {
		http.Error(w, "Token exchange failed", http.StatusInternalServerError)
		return
	}

	client := h.conf.Client(ctx, oauthToken)
	resp, err := client.Get("https://api.github.com/user")
	if err != nil {
		http.Error(w, "Failed to get user info", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	var body struct {
		Login string `json:"login"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		http.Error(w, "Failed to parse user info", http.StatusInternalServerError)
		return
	}
	if body.Login == "" {
		http.Error(w, "Invalid user info from GitHub", http.StatusInternalServerError)
		return
	}

	token, err := h.tokenManager.Generate(body.Login)
	if err != nil {
		http.Error(w, "Failed to create token", http.StatusInternalServerError)
		return
	}

	h.setCookie(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusTemporaryRedirect)
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
