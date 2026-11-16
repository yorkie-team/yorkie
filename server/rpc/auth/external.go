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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gopkg.in/yaml.v3"

	"github.com/yorkie-team/yorkie/internal/validation"
)

const (
	// externalAuthProviderPrefix prefixes the auth provider of users signed
	// in by an external provider, so no configured name can collide with
	// "github" or local users.
	externalAuthProviderPrefix = "external:"

	// maxAssertionLifetime is the longest exp - iat an assertion may have.
	maxAssertionLifetime = 60 * time.Second

	// assertionLeeway absorbs clock skew between the broker and the server.
	assertionLeeway = 5 * time.Second

	// maxCallbackBody caps the callback form, which anyone can post.
	maxCallbackBody = 64 << 10
)

var (
	// ErrInvalidExternalProvider is returned when an external provider
	// config is invalid.
	ErrInvalidExternalProvider = errors.New("invalid external provider")

	errAssertionMissing  = fmt.Errorf("no assertion in the callback: %w", errLoginRejected)
	errAssertionInvalid  = fmt.Errorf("invalid assertion: %w", errLoginRejected)
	errAssertionReplayed = fmt.Errorf("assertion already used: %w", errLoginRejected)

	providerNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
)

// ExternalProviderConfig is the configuration of a login broker run outside
// Yorkie. The broker signs users in with whatever its organization uses and
// posts an ES256 JWT assertion to the callback.
type ExternalProviderConfig struct {
	// Name identifies the provider in URLs and in the auth provider of its
	// users: lowercase letters, digits and hyphens.
	Name string `yaml:"Name"`

	// DisplayName is shown on the dashboard login button.
	DisplayName string `yaml:"DisplayName"`

	// LoginURL is the broker URL that starts a login. Yorkie adds `state`.
	LoginURL string `yaml:"LoginURL"`

	// Issuer must equal the `iss` of assertions.
	Issuer string `yaml:"Issuer"`

	// Audience must be in the `aud` of assertions.
	Audience string `yaml:"Audience"`

	// PublicKeys are PEM-encoded P-256 public keys. An assertion signed by
	// any of them is accepted, so a broker can rotate its key.
	PublicKeys []string `yaml:"PublicKeys"`
}

// Validate validates the config and parses its public keys.
func (c *ExternalProviderConfig) Validate() error {
	_, err := c.parse()
	return err
}

// parse validates the config and returns its public keys.
func (c *ExternalProviderConfig) parse() ([]*ecdsa.PublicKey, error) {
	if !providerNamePattern.MatchString(c.Name) {
		return nil, fmt.Errorf(
			"name %q: lowercase letters, digits and hyphens, up to 32: %w", c.Name, ErrInvalidExternalProvider,
		)
	}
	if c.DisplayName == "" {
		return nil, fmt.Errorf("%s: no DisplayName: %w", c.Name, ErrInvalidExternalProvider)
	}
	// The state travels in the LoginURL query, so it must not cross the
	// network in the clear, except to a broker on this machine.
	loginURL, err := url.Parse(c.LoginURL)
	if err != nil || loginURL.Host == "" ||
		(loginURL.Scheme != "https" && (loginURL.Scheme != "http" || !isLoopback(loginURL.Hostname()))) {
		return nil, fmt.Errorf(
			"%s: LoginURL %q: https, or http on localhost: %w", c.Name, c.LoginURL, ErrInvalidExternalProvider,
		)
	}
	if c.Issuer == "" || c.Audience == "" {
		return nil, fmt.Errorf("%s: no Issuer or Audience: %w", c.Name, ErrInvalidExternalProvider)
	}
	if len(c.PublicKeys) == 0 {
		return nil, fmt.Errorf("%s: no PublicKeys: %w", c.Name, ErrInvalidExternalProvider)
	}

	keys := make([]*ecdsa.PublicKey, 0, len(c.PublicKeys))
	for i, encoded := range c.PublicKeys {
		key, err := parseP256PublicKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("%s: PublicKeys[%d]: %v: %w", c.Name, i, err, ErrInvalidExternalProvider)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// LoadExternalProviders reads external provider configs from a YAML file
// holding a list of them, the shape of Auth.ExternalProviders in a config
// file.
func LoadExternalProviders(path string) ([]ExternalProviderConfig, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read external providers: %w", err)
	}

	var providers []ExternalProviderConfig
	if err := yaml.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("parse external providers %s: %w", path, err)
	}
	return providers, nil
}

// isLoopback reports whether the given host is this machine.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// parseP256PublicKey parses a PEM-encoded PKIX P-256 public key.
func parseP256PublicKey(encoded string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, errors.New("not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("not a P-256 public key")
	}
	return key, nil
}

// assertionClaims are the claims of an assertion a broker signs.
type assertionClaims struct {
	jwt.RegisteredClaims
	State             string `json:"state"`
	PreferredUsername string `json:"preferred_username"`
}

// externalProvider signs users in with assertions from a login broker.
type externalProvider struct {
	conf   ExternalProviderConfig
	keys   jwt.VerificationKeySet
	parser *jwt.Parser

	// now returns the current time. Tests replace it.
	now func() time.Time

	// usedIDs maps the `jti` of accepted assertions to their expiry.
	mu      sync.Mutex
	usedIDs map[string]time.Time
}

// newExternalProvider creates a provider for the given config.
func newExternalProvider(conf ExternalProviderConfig) (*externalProvider, error) {
	keys, err := conf.parse()
	if err != nil {
		return nil, err
	}

	p := &externalProvider{
		conf:    conf,
		now:     time.Now,
		usedIDs: make(map[string]time.Time),
	}
	for _, key := range keys {
		p.keys.Keys = append(p.keys.Keys, key)
	}
	p.parser = jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodES256.Alg()}),
		jwt.WithIssuer(conf.Issuer),
		jwt.WithAudience(conf.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(assertionLeeway),
		jwt.WithTimeFunc(func() time.Time { return p.now() }),
	)
	return p, nil
}

// Name returns the auth provider of the users of this provider.
func (p *externalProvider) Name() string {
	return externalAuthProviderPrefix + p.conf.Name
}

// LoginURL returns the broker login URL carrying the given state.
func (p *externalProvider) LoginURL(state string) string {
	// Validated by newExternalProvider.
	u, _ := url.Parse(p.conf.LoginURL)
	query := u.Query()
	query.Set("state", state)
	u.RawQuery = query.Encode()
	return u.String()
}

// Callback verifies the assertion the broker posted and returns its user.
func (p *externalProvider) Callback(
	_ context.Context,
	r *http.Request,
	consumeState func(state string) bool,
) (*Identity, error) {
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("%s: %w", r.Method, errAssertionMissing)
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil ||
		mediaType != "application/x-www-form-urlencoded" {
		return nil, fmt.Errorf("content type %q: %w", r.Header.Get("Content-Type"), errAssertionMissing)
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxCallbackBody)
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("%w: %v", errAssertionMissing, err)
	}
	assertion := r.PostForm.Get("assertion")
	if assertion == "" {
		return nil, errAssertionMissing
	}

	claims := &assertionClaims{}
	if _, err := p.parser.ParseWithClaims(assertion, claims, func(*jwt.Token) (any, error) {
		return p.keys, nil
	}); err != nil {
		return nil, fmt.Errorf("%w: %v", errAssertionInvalid, err)
	}
	if err := p.checkClaims(claims); err != nil {
		return nil, err
	}

	// The signature holds, so the state is the one the broker received.
	if !consumeState(claims.State) {
		return nil, errInvalidState
	}
	if err := p.useID(claims.ID, claims.ExpiresAt.Time); err != nil {
		return nil, err
	}

	username := claims.PreferredUsername
	if username == "" {
		username = claims.Subject
	}
	if !validUsername(username) {
		return nil, fmt.Errorf("%q: %w", username, errInvalidUsername)
	}
	return &Identity{Subject: claims.Subject, Username: username}, nil
}

// checkClaims checks the claims the parser does not.
func (p *externalProvider) checkClaims(claims *assertionClaims) error {
	if claims.Subject == "" || claims.ID == "" || claims.State == "" {
		return fmt.Errorf("%w: sub, jti and state are required", errAssertionInvalid)
	}
	if claims.IssuedAt == nil {
		return fmt.Errorf("%w: iat is required", errAssertionInvalid)
	}
	if claims.ExpiresAt.Sub(claims.IssuedAt.Time) > maxAssertionLifetime {
		return fmt.Errorf("%w: lives longer than %s", errAssertionInvalid, maxAssertionLifetime)
	}
	return nil
}

// useID records the given `jti` until the given expiry and refuses one that
// was already used.
func (p *externalProvider) useID(id string, expiresAt time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	for used, exp := range p.usedIDs {
		if now.After(exp.Add(assertionLeeway)) {
			delete(p.usedIDs, used)
		}
	}

	if _, ok := p.usedIDs[id]; ok {
		return errAssertionReplayed
	}
	p.usedIDs[id] = expiresAt
	return nil
}

// validUsername reports whether the given name can be a Yorkie username. The
// rule is the one types.UserFields applies at sign-up.
func validUsername(name string) bool {
	return validation.ValidateValue(name, "required,min=2,max=30,slug") == nil
}
