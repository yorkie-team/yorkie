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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lithammer/shortuuid/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
)

// fakeBroker signs assertions the way an operator's login broker does.
type fakeBroker struct {
	key *ecdsa.PrivateKey
}

func newFakeBroker(t *testing.T) *fakeBroker {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return &fakeBroker{key: key}
}

// publicKeyPEM returns the public key of the broker for the server config.
func (b *fakeBroker) publicKeyPEM(t *testing.T) string {
	return encodePublicKey(t, &b.key.PublicKey)
}

func encodePublicKey(t *testing.T, key any) string {
	der, err := x509.MarshalPKIXPublicKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// claims returns valid claims for the given state and subject.
func (b *fakeBroker) claims(state, subject string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":                "test-broker",
		"aud":                "yorkie-admin",
		"sub":                subject,
		"state":              state,
		"iat":                now.Unix(),
		"exp":                now.Add(30 * time.Second).Unix(),
		"jti":                shortuuid.New(),
		"preferred_username": "alice",
	}
}

// sign signs the given claims with the broker key.
func (b *fakeBroker) sign(t *testing.T, claims jwt.MapClaims) string {
	signed, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(b.key)
	require.NoError(t, err)
	return signed
}

// externalConfig returns a provider config named corp trusting the given
// brokers.
func externalConfig(t *testing.T, brokers ...*fakeBroker) ExternalProviderConfig {
	conf := ExternalProviderConfig{
		Name:        "corp",
		DisplayName: "Company SSO",
		LoginURL:    "https://login-broker.example.com/login?tenant=yorkie",
		Issuer:      "test-broker",
		Audience:    "yorkie-admin",
	}
	for _, broker := range brokers {
		conf.PublicKeys = append(conf.PublicKeys, broker.publicKeyPEM(t))
	}
	return conf
}

// newExternalAuthHandler returns an auth manager on a memory backend with
// the given external provider and no GitHub app.
func newExternalAuthHandler(t *testing.T, conf ExternalProviderConfig) *AuthManager {
	db, err := memory.New()
	require.NoError(t, err)

	_, handler, err := NewAuthHandler(
		&backend.Backend{DB: db},
		NewTokenManager("test-secret-key", time.Hour),
		Config{
			GitHubCallbackRedirectURL: "http://localhost/dashboard",
			ExternalProviders:         []ExternalProviderConfig{conf},
		},
	)
	require.NoError(t, err)
	return handler.(*AuthManager)
}

// startExternalLogin starts a login on the corp provider and returns the
// state it sent to the broker.
func startExternalLogin(t *testing.T, manager *AuthManager) string {
	login := httptest.NewRecorder()
	manager.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/external/corp/login", nil))
	require.Equal(t, http.StatusTemporaryRedirect, login.Code)

	location, err := url.Parse(login.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, "login-broker.example.com", location.Host)
	assert.Equal(t, "yorkie", location.Query().Get("tenant"), "dropped the query of LoginURL")

	state := location.Query().Get("state")
	require.NotEmpty(t, state)
	return state
}

// postAssertion posts the assertion to the corp callback from a browser
// whose state cookie holds browserState.
func postAssertion(manager *AuthManager, assertion, browserState string) *http.Response {
	form := url.Values{"assertion": {assertion}}
	r := httptest.NewRequest(http.MethodPost, "/auth/external/corp/callback", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if browserState != "" {
		r = withStateCookie(r, browserState)
	}

	rec := httptest.NewRecorder()
	manager.ServeHTTP(rec, r)
	return rec.Result()
}

// signInExternal runs a whole login with claims the given function edits.
func signInExternal(
	t *testing.T,
	manager *AuthManager,
	broker *fakeBroker,
	subject string,
	edit func(jwt.MapClaims),
) *http.Response {
	state := startExternalLogin(t, manager)
	claims := broker.claims(state, subject)
	if edit != nil {
		edit(claims)
	}
	return postAssertion(manager, broker.sign(t, claims), state)
}

func TestExternalProvider(t *testing.T) {
	t.Run("signs a new user in and finds them again by subject", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		resp := signInExternal(t, manager, broker, "employee-1001", nil)
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
		assert.Equal(t, "http://localhost/dashboard", resp.Header.Get("Location"))
		assert.True(t, hasSessionCookie(resp))

		user, err := manager.be.DB.FindUserInfoByName(t.Context(), "alice")
		require.NoError(t, err)
		assert.Equal(t, "external:corp", user.AuthProvider)
		assert.Equal(t, "employee-1001", user.Subject)

		again := signInExternal(t, manager, broker, "employee-1001", func(c jwt.MapClaims) {
			c["preferred_username"] = "alice-renamed"
		})
		require.Equal(t, http.StatusSeeOther, again.StatusCode)
		_, err = manager.be.DB.FindUserInfoByName(t.Context(), "alice-renamed")
		assert.Error(t, err, "renamed a returning user")
	})

	t.Run("uses the subject as username without preferred_username", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		resp := signInExternal(t, manager, broker, "employee-1002", func(c jwt.MapClaims) {
			delete(c, "preferred_username")
		})
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)
		_, err := manager.be.DB.FindUserInfoByName(t.Context(), "employee-1002")
		assert.NoError(t, err)
	})

	t.Run("refuses a username another account holds", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))
		_, err := manager.be.DB.CreateUserInfo(t.Context(), "alice", "hashed-password")
		require.NoError(t, err)

		resp := signInExternal(t, manager, broker, "employee-1001", nil)
		assert.Equal(t, http.StatusConflict, resp.StatusCode)
		assert.False(t, hasSessionCookie(resp))
	})

	t.Run("refuses a username Yorkie does not allow", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		resp := signInExternal(t, manager, broker, "employee-1001", func(c jwt.MapClaims) {
			c["preferred_username"] = "alice@example.com"
		})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.False(t, hasSessionCookie(resp))
	})

	t.Run("accepts assertions signed by any configured key", func(t *testing.T) {
		oldBroker, newBroker := newFakeBroker(t), newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, oldBroker, newBroker))

		assert.Equal(t, http.StatusSeeOther,
			signInExternal(t, manager, oldBroker, "employee-1001", nil).StatusCode)
		assert.Equal(t, http.StatusSeeOther,
			signInExternal(t, manager, newBroker, "employee-1001", nil).StatusCode)
	})

	t.Run("rejects an assertion that does not verify", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		cases := map[string]func(jwt.MapClaims){
			"expired": func(c jwt.MapClaims) {
				c["iat"] = time.Now().Add(-2 * time.Minute).Unix()
				c["exp"] = time.Now().Add(-90 * time.Second).Unix()
			},
			"lives too long": func(c jwt.MapClaims) {
				c["exp"] = time.Now().Add(10 * time.Minute).Unix()
			},
			"issued in the future": func(c jwt.MapClaims) {
				c["iat"] = time.Now().Add(time.Minute).Unix()
				c["exp"] = time.Now().Add(80 * time.Second).Unix()
			},
			"not valid yet":  func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Minute).Unix() },
			"no state":       func(c jwt.MapClaims) { delete(c, "state") },
			"no exp":         func(c jwt.MapClaims) { delete(c, "exp") },
			"no iat":         func(c jwt.MapClaims) { delete(c, "iat") },
			"no sub":         func(c jwt.MapClaims) { delete(c, "sub") },
			"no jti":         func(c jwt.MapClaims) { delete(c, "jti") },
			"wrong audience": func(c jwt.MapClaims) { c["aud"] = "another-service" },
			"wrong issuer":   func(c jwt.MapClaims) { c["iss"] = "another-broker" },
		}
		for name, edit := range cases {
			resp := signInExternal(t, manager, broker, "employee-1001", edit)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
			assert.False(t, hasSessionCookie(resp), name)
		}
	})

	t.Run("rejects an assertion signed another way", func(t *testing.T) {
		broker := newFakeBroker(t)
		stranger := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)

		signers := map[string]func(jwt.MapClaims) string{
			"unknown key": func(c jwt.MapClaims) string { return stranger.sign(t, c) },
			"RS256": func(c jwt.MapClaims) string {
				signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, c).SignedString(rsaKey)
				require.NoError(t, err)
				return signed
			},
			"HS256 keyed with the public key": func(c jwt.MapClaims) string {
				signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).
					SignedString([]byte(broker.publicKeyPEM(t)))
				require.NoError(t, err)
				return signed
			},
			"none": func(c jwt.MapClaims) string {
				signed, err := jwt.NewWithClaims(jwt.SigningMethodNone, c).
					SignedString(jwt.UnsafeAllowNoneSignatureType)
				require.NoError(t, err)
				return signed
			},
		}
		for name, sign := range signers {
			state := startExternalLogin(t, manager)
			resp := postAssertion(manager, sign(broker.claims(state, "employee-1001")), state)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
			assert.False(t, hasSessionCookie(resp), name)
		}
	})

	t.Run("rejects a reused assertion ID", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		resp := signInExternal(t, manager, broker, "employee-1001", func(c jwt.MapClaims) { c["jti"] = "once" })
		require.Equal(t, http.StatusSeeOther, resp.StatusCode)

		replay := signInExternal(t, manager, broker, "employee-1001", func(c jwt.MapClaims) { c["jti"] = "once" })
		assert.Equal(t, http.StatusUnauthorized, replay.StatusCode)
		assert.False(t, hasSessionCookie(replay))
	})

	t.Run("rejects a state the server did not send or the browser did not start", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		state := startExternalLogin(t, manager)
		forged := postAssertion(manager, broker.sign(t, broker.claims("forged", "employee-1001")), "forged")
		assert.Equal(t, http.StatusBadRequest, forged.StatusCode)

		forwarded := postAssertion(manager, broker.sign(t, broker.claims(state, "employee-1001")), "")
		assert.Equal(t, http.StatusBadRequest, forwarded.StatusCode)
		assert.False(t, hasSessionCookie(forwarded))
	})

	t.Run("rejects a state issued for GitHub", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))

		login := httptest.NewRecorder()
		manager.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/auth/github/login", nil))
		location, err := url.Parse(login.Header().Get("Location"))
		require.NoError(t, err)
		githubState := location.Query().Get("state")

		resp := postAssertion(manager, broker.sign(t, broker.claims(githubState, "employee-1001")), githubState)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("rejects an oversized or non-form callback body", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))
		state := startExternalLogin(t, manager)
		assertion := broker.sign(t, broker.claims(state, "employee-1001"))

		big := url.Values{"assertion": {assertion}, "pad": {strings.Repeat("a", maxCallbackBody)}}
		r := httptest.NewRequest(http.MethodPost, "/auth/external/corp/callback", strings.NewReader(big.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		manager.ServeHTTP(rec, withStateCookie(r, state))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)

		r = httptest.NewRequest(http.MethodPost, "/auth/external/corp/callback",
			strings.NewReader("--b\r\nContent-Disposition: form-data; name=\"assertion\"\r\n\r\n"+assertion+"\r\n--b--\r\n"))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=b")
		rec = httptest.NewRecorder()
		manager.ServeHTTP(rec, withStateCookie(r, state))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("rejects a callback without a posted assertion", func(t *testing.T) {
		broker := newFakeBroker(t)
		manager := newExternalAuthHandler(t, externalConfig(t, broker))
		state := startExternalLogin(t, manager)
		assertion := broker.sign(t, broker.claims(state, "employee-1001"))

		get := httptest.NewRecorder()
		manager.ServeHTTP(get, withStateCookie(httptest.NewRequest(
			http.MethodGet, "/auth/external/corp/callback?assertion="+url.QueryEscape(assertion), nil,
		), state))
		assert.Equal(t, http.StatusUnauthorized, get.Code)

		empty := postAssertion(manager, "", state)
		assert.Equal(t, http.StatusUnauthorized, empty.StatusCode)
	})
}

func TestExternalProviderConfig(t *testing.T) {
	broker := newFakeBroker(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	p384Key, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	require.NoError(t, err)

	t.Run("accepts a valid config", func(t *testing.T) {
		conf := externalConfig(t, broker)
		assert.NoError(t, conf.Validate())

		for _, loginURL := range []string{"http://localhost:9000/login", "http://127.0.0.1/login"} {
			conf.LoginURL = loginURL
			assert.NoError(t, conf.Validate(), loginURL)
		}
	})

	t.Run("refuses an invalid config", func(t *testing.T) {
		rsaPEM := encodePublicKey(t, &rsaKey.PublicKey)
		p384PEM := encodePublicKey(t, &p384Key.PublicKey)
		cases := map[string]func(*ExternalProviderConfig){
			"empty name":       func(c *ExternalProviderConfig) { c.Name = "" },
			"name with slash":  func(c *ExternalProviderConfig) { c.Name = "corp/sso" },
			"uppercase name":   func(c *ExternalProviderConfig) { c.Name = "Corp" },
			"no display name":  func(c *ExternalProviderConfig) { c.DisplayName = "" },
			"relative login":   func(c *ExternalProviderConfig) { c.LoginURL = "/login" },
			"plain http login": func(c *ExternalProviderConfig) { c.LoginURL = "http://login-broker.example.com/login" },
			"no issuer":        func(c *ExternalProviderConfig) { c.Issuer = "" },
			"no audience":      func(c *ExternalProviderConfig) { c.Audience = "" },
			"no keys":          func(c *ExternalProviderConfig) { c.PublicKeys = nil },
			"not PEM":          func(c *ExternalProviderConfig) { c.PublicKeys = []string{"not a key"} },
			"RSA key":          func(c *ExternalProviderConfig) { c.PublicKeys = []string{rsaPEM} },
			"P-384 key":        func(c *ExternalProviderConfig) { c.PublicKeys = []string{p384PEM} },
			"one bad key of 2": func(c *ExternalProviderConfig) { c.PublicKeys = append(c.PublicKeys, "not a key") },
		}
		for name, edit := range cases {
			conf := externalConfig(t, broker)
			edit(&conf)
			assert.ErrorIs(t, conf.Validate(), ErrInvalidExternalProvider, name)
		}
	})

	t.Run("loads the list a file holds, as the chart renders it", func(t *testing.T) {
		key := strings.ReplaceAll(strings.TrimSpace(broker.publicKeyPEM(t)), "\n", "\n    ")
		path := filepath.Join(t.TempDir(), "external-providers.yaml")
		require.NoError(t, os.WriteFile(path, []byte(`- Audience: yorkie-admin
  DisplayName: Company SSO
  Issuer: test-broker
  LoginURL: https://login-broker.example.com/login
  Name: corp
  PublicKeys:
  - |
    `+key+`
`), 0o600))

		providers, err := LoadExternalProviders(path)
		require.NoError(t, err)
		require.Len(t, providers, 1)
		assert.Equal(t, "corp", providers[0].Name)
		assert.NoError(t, providers[0].Validate())

		_, err = LoadExternalProviders(filepath.Join(t.TempDir(), "missing.yaml"))
		assert.Error(t, err)
	})

	t.Run("refuses two providers of one name", func(t *testing.T) {
		conf := Config{ExternalProviders: []ExternalProviderConfig{externalConfig(t, broker), externalConfig(t, broker)}}
		assert.ErrorIs(t, conf.Validate(), ErrInvalidExternalProvider)
	})

	t.Run("cannot share accounts with GitHub by its name", func(t *testing.T) {
		conf := externalConfig(t, broker)
		conf.Name = "github"
		provider, err := newExternalProvider(conf)
		require.NoError(t, err)
		assert.NotEqual(t, "github", provider.Name())
	})
}

func TestProvidersEndpoint(t *testing.T) {
	readProviders := func(t *testing.T, handler http.Handler) (bool, []listedProvider) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/providers", nil))
		require.Equal(t, http.StatusOK, rec.Code)

		var body struct {
			PasswordLogin bool             `json:"passwordLogin"`
			Providers     []listedProvider `json:"providers"`
		}
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		require.NotNil(t, body.Providers, "providers must be a list, not null")
		return body.PasswordLogin, body.Providers
	}

	t.Run("lists GitHub only when configured, then external providers", func(t *testing.T) {
		withGitHub, _ := newGitHubAuthHandler(t, "octocat")
		password, providers := readProviders(t, withGitHub)
		assert.True(t, password)
		assert.Equal(t, []listedProvider{
			{Name: "github", DisplayName: "GitHub", LoginPath: "/auth/github/login"},
		}, providers)

		external := newExternalAuthHandler(t, externalConfig(t, newFakeBroker(t)))
		_, providers = readProviders(t, external)
		assert.Equal(t, []listedProvider{
			{Name: "corp", DisplayName: "Company SSO", LoginPath: "/auth/external/corp/login"},
		}, providers)
	})

	t.Run("answers an empty list with no provider", func(t *testing.T) {
		db, err := memory.New()
		require.NoError(t, err)
		_, handler, err := NewAuthHandler(&backend.Backend{DB: db}, NewTokenManager("k", time.Hour), Config{})
		require.NoError(t, err)

		_, providers := readProviders(t, handler)
		assert.Empty(t, providers)
	})
}
