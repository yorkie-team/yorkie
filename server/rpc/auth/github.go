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
	"net/http"

	"golang.org/x/oauth2"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	errGitHubTokenExchange = errors.New("token exchange failed")
	errGitHubUserRequest   = errors.New("failed to get user info")
	errGitHubUserDecode    = errors.New("failed to parse user info")
	errGitHubUserInvalid   = errors.New("invalid user info from GitHub")
)

// githubProvider signs users in with a GitHub OAuth app.
type githubProvider struct {
	oauth2Conf *oauth2.Config
	userAPIURL string
}

// newGitHubProvider creates a provider for the GitHub OAuth app of the config.
func newGitHubProvider(conf Config) *githubProvider {
	return &githubProvider{
		oauth2Conf: &oauth2.Config{
			ClientID:     conf.GitHubClientID,
			ClientSecret: conf.GitHubClientSecret,
			RedirectURL:  conf.GitHubRedirectURL,
			Scopes:       []string{"user:email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:       conf.GitHubAuthURL,
				TokenURL:      conf.GitHubTokenURL,
				DeviceAuthURL: conf.GitHubDeviceAuthURL,
			},
		},
		userAPIURL: conf.GitHubUserURL,
	}
}

// Name returns the auth provider of GitHub users.
func (p *githubProvider) Name() string {
	return database.AuthProviderGitHub
}

// LoginURL returns the GitHub authorize URL carrying the given state.
func (p *githubProvider) LoginURL(state string) string {
	return p.oauth2Conf.AuthCodeURL(state)
}

// Callback exchanges the code of the callback for a token and reads the
// GitHub login of the user with it.
func (p *githubProvider) Callback(
	ctx context.Context,
	r *http.Request,
	consumeState func(state string) bool,
) (*Identity, error) {
	if !consumeState(r.URL.Query().Get("state")) {
		return nil, errInvalidState
	}

	oauthToken, err := p.oauth2Conf.Exchange(ctx, r.URL.Query().Get("code"))
	if err != nil {
		return nil, errGitHubTokenExchange
	}

	// The URL is the configured GitHub user API, not request input.
	resp, err := p.oauth2Conf.Client(ctx, oauthToken).Get(p.userAPIURL) //nolint:gosec
	if err != nil {
		return nil, errGitHubUserRequest
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logging.DefaultLogger().Error(err)
		}
	}()

	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, errGitHubUserDecode
	}
	if body.Login == "" {
		return nil, errGitHubUserInvalid
	}

	return &Identity{Username: body.Login}, nil
}
