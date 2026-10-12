/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package mongo

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/yorkie-team/yorkie/pkg/cache"
)

// Below are the default values applied when the matching fields are left
// empty, so that a Config built in code (not only one read from a config
// file) resolves to a usable value.
const (
	// DefaultProjectCacheSize is the default size of the project metadata cache.
	DefaultProjectCacheSize = 256

	// DefaultProjectCacheTTL is the default TTL of the project metadata cache.
	DefaultProjectCacheTTL = 10 * time.Minute

	// DefaultCacheStatsInterval is the default interval for logging cache statistics.
	DefaultCacheStatsInterval = 30 * time.Second
)

// Config is the configuration for creating a Client instance.
type Config struct {
	ConnectionTimeout string `yaml:"ConnectionTimeout"`
	ConnectionURI     string `yaml:"ConnectionURI"`
	YorkieDatabase    string `yaml:"YorkieDatabase"`
	PingTimeout       string `yaml:"PingTimeout"`

	// MonitoringEnabled determines whether query monitoring is enabled.
	MonitoringEnabled bool `yaml:"MonitoringEnabled"`

	// MonitoringSlowQueryThreshold is the threshold in milliseconds to log slow queries.
	// If a query takes longer than this threshold, it will be logged as a slow query.
	MonitoringSlowQueryThreshold string `yaml:"MonitoringSlowQueryThreshold"`

	// CacheStatsEnabled determines whether cache statistics logging is enabled.
	CacheStatsEnabled bool `yaml:"CacheStatsEnabled"`

	// CacheStatsInterval is the interval for logging cache statistics.
	CacheStatsInterval string `yaml:"CacheStatsInterval"`

	// ClientCacheSize is the size of the client cache. It works as LRU cache.
	ClientCacheSize int `yaml:"ClientCacheSize"`

	// DocCacheSize is the size of the document cache. It works as LRU cache.
	DocCacheSize int `yaml:"DocCacheSize"`

	// ChangeCacheSize is the size of the change cache. It works as LRU cache.
	ChangeCacheSize int `yaml:"ChangeCacheSize"`

	// ActorCacheSize is the size of the actor cache. It works as LRU cache.
	VectorCacheSize int `yaml:"VectorCacheSize"`

	// ProjectCacheSize is the size of the project metadata cache.
	ProjectCacheSize int `yaml:"ProjectCacheSize"`

	// ProjectCacheTTL is the TTL value for the project metadata cache.
	ProjectCacheTTL string `yaml:"ProjectCacheTTL"`
}

// Validate returns an error if the provided Config is invalidated.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.ConnectionTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--mongo-connection-timeout" flag: %w`,
			c.ConnectionTimeout,
			err,
		)
	}

	if _, err := time.ParseDuration(c.PingTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--mongo-ping-timeout" flag: %w`,
			c.PingTimeout,
			err,
		)
	}

	if c.CacheStatsInterval != "" {
		if _, err := time.ParseDuration(c.CacheStatsInterval); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for cache stats interval: %w`,
				c.CacheStatsInterval,
				err,
			)
		}
	}

	if c.ProjectCacheSize < 0 {
		return fmt.Errorf(
			`invalid argument "%d" for "--mongo-project-cache-size" flag: size must not be negative`,
			c.ProjectCacheSize,
		)
	}

	// An empty value is not an error here, because ParseProjectCacheTTL falls
	// back to DefaultProjectCacheTTL instead of failing.
	if c.ProjectCacheTTL != "" {
		if _, err := cache.ParseTTL(c.ProjectCacheTTL); err != nil {
			return fmt.Errorf(
				`invalid argument "%s" for "--mongo-project-cache-ttl" flag: %w`,
				c.ProjectCacheTTL,
				err,
			)
		}
	}

	return nil
}

// unparsableConnectionURI stands in for a connection URI whose shape this file
// cannot make out. The userinfo of such a URI cannot be located and therefore
// cannot be stripped, so none of it is reported.
const unparsableConnectionURI = "(unparsable URI)"

// redactionMask is what a masked value is replaced with. It matches the mask
// url.URL.Redacted applies to the userinfo password, so a line carrying both
// kinds of secret reads consistently.
const redactionMask = "xxxxx"

// credentialOptionSubstrings match the name of a connection-string option that
// says outright that its value is a secret. Matching on a substring rather than
// on an exact name also covers the spellings the MongoDB drivers accept as
// aliases (for example sslClientCertificateKeyPassword for
// tlsCertificateKeyFilePassword) and any option added after this was written.
var credentialOptionSubstrings = []string{"password", "passwd", "token", "secret", "credential"}

// credentialOptionNames are the options whose value carries a secret without
// saying so in the name: authMechanismProperties holds, among others, the
// AWS_SESSION_TOKEN and the OIDC access token.
var credentialOptionNames = map[string]bool{
	"authmechanismproperties": true,
}

// isCredentialOption reports whether the named connection-string option carries
// a secret. Option names are compared case-insensitively, as the MongoDB
// connection-string spec treats them.
func isCredentialOption(name string) bool {
	lowered := strings.ToLower(name)
	if credentialOptionNames[lowered] {
		return true
	}

	for _, substring := range credentialOptionSubstrings {
		if strings.Contains(lowered, substring) {
			return true
		}
	}

	return false
}

// optionDelimiters are the characters that open an option string or separate
// one option from the next. '?' opens it and '&' separates, while ';' is the
// legacy separator the MongoDB drivers still accept. Splitting on all three
// finds every option without having to know where the authority ends, which an
// unescaped character in the password can otherwise hide.
const optionDelimiters = "?&;"

// redactOption masks the value of option when its name says the value is a
// secret, leaving the spelling of the name untouched so the logged option
// string still reads like the configured one.
func redactOption(option string) string {
	name, _, hasValue := strings.Cut(option, "=")
	if !hasValue {
		return option
	}

	decoded, err := url.QueryUnescape(name)
	if err != nil {
		// An option whose name does not decode cannot be matched against the
		// lists above, so it is masked rather than trusted.
		return name + "=" + redactionMask
	}

	if isCredentialOption(decoded) {
		return name + "=" + redactionMask
	}

	return option
}

// redactOptions masks the value of every credential-bearing option in s,
// keeping the order and the delimiters so the logged URI still reads like the
// configured one.
func redactOptions(s string) string {
	var redacted strings.Builder
	for {
		i := strings.IndexAny(s, optionDelimiters)
		if i < 0 {
			redacted.WriteString(redactOption(s))
			return redacted.String()
		}

		redacted.WriteString(redactOption(s[:i]))
		redacted.WriteByte(s[i])
		s = s[i+1:]
	}
}

// hostListChars are the characters a host list is spelled with: a host name or
// address, an optional port, the ',' between the hosts of a replica set, the
// '[' and ']' of an IPv6 literal and the '%' of a percent-encoded unix-socket
// path. Anything else means the segment is not a host list.
const hostListChars = ".-_:,[]%"

// isHostList reports whether s, the segment that follows a candidate '@', is
// spelled like the host list of a connection URI. It is how an '@' that closes
// the userinfo is told from one that merely sits in an option value.
func isHostList(s string) bool {
	if s == "" {
		return false
	}

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(hostListChars, r):
		default:
			return false
		}
	}

	return true
}

// cutUserinfo splits rest, the connection URI after its scheme, into its
// userinfo and what follows. The split is made on the raw string rather than
// through net/url, because net/url cuts the fragment and the query off before
// it looks for the authority: in mongodb://user:pa#ss@host:27017 an unescaped
// '#' in the password leaves it reading "user:pa" as the host and
// "ss@host:27017" as the fragment, so there is no userinfo left for
// URL.Redacted to mask and the password is echoed in full.
//
// Candidates are tried from the right, as the connection-string spec has the
// last '@' close the userinfo. One whose userinfo would span a '/' is skipped,
// since a '/' ends the authority and so puts that '@' in the path or in the
// option string.
func cutUserinfo(rest string) (string, string, bool) {
	for at := strings.LastIndex(rest, "@"); at >= 0; at = strings.LastIndex(rest[:at], "@") {
		if strings.Contains(rest[:at], "/") {
			continue
		}

		if isHostList(hostList(rest[at+1:])) {
			return rest[:at], rest[at+1:], true
		}
	}

	return "", rest, false
}

// hostList returns the leading segment of s that holds the hosts: the
// authority ends at the first '/', '?' or '#' that follows it.
func hostList(s string) string {
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		return s[:i]
	}

	return s
}

// maskUserinfo masks the password of userinfo, keeping the username so the
// line still says which user the server connects as. The first ':' is read as
// the separator, so an unescaped ':' in the username masks more than the
// password rather than less.
func maskUserinfo(userinfo string) string {
	username, _, hasPassword := strings.Cut(userinfo, ":")
	if !hasPassword {
		return userinfo
	}

	return username + ":" + redactionMask
}

// isControl reports whether r is an ASCII control character. Such a character
// has no place in a connection URI, and one that reached a log line could forge
// a line of its own.
func isControl(r rune) bool {
	return r < ' ' || r == 0x7f
}

// RedactedConnectionURI returns the connection URI with every credential it
// carries masked: the password of its userinfo and the value of any
// credential-bearing connection-string option, such as
// tlsCertificateKeyFilePassword, proxyPassword or authMechanismProperties. The
// URI carries the database user's password, so logging it verbatim publishes
// that credential to everyone who can read the server's stdout — a far weaker
// permission than the one guarding the secret it is deployed in. Scheme,
// username, hosts and the remaining options are kept, since those are what make
// the line worth logging.
//
// A URI whose credentials cannot be located is reported as unparsable instead
// of in part: masking too much costs a log line, masking too little publishes
// the password.
func (c *Config) RedactedConnectionURI() string {
	scheme, rest, hasScheme := strings.Cut(c.ConnectionURI, "://")
	if !hasScheme || scheme == "" ||
		strings.ContainsAny(scheme, "/?#@") ||
		strings.ContainsFunc(c.ConnectionURI, isControl) {
		return unparsableConnectionURI
	}

	if userinfo, remainder, ok := cutUserinfo(rest); ok {
		return scheme + "://" + maskUserinfo(userinfo) + "@" + redactOptions(remainder)
	}

	redacted := redactOptions(rest)
	if strings.Contains(redacted, "@") {
		// An '@' that closes no userinfo this can locate and belongs to no
		// option masked above: the URI may still carry a password, spelled in
		// a way this cannot find, so none of it is reported.
		return unparsableConnectionURI
	}

	return scheme + "://" + redacted
}

// ParseConnectionTimeout returns connection timeout duration.
func (c *Config) ParseConnectionTimeout() time.Duration {
	result, err := time.ParseDuration(c.ConnectionTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse connection timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParsePingTimeout returns ping timeout duration.
func (c *Config) ParsePingTimeout() time.Duration {
	result, err := time.ParseDuration(c.PingTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse ping timeout: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseCacheStatsInterval returns cache stats interval duration, falling back
// to DefaultCacheStatsInterval when the value is unset. A Config built in code
// may leave it empty, and an unset value must not terminate the process.
func (c *Config) ParseCacheStatsInterval() time.Duration {
	if c.CacheStatsInterval == "" {
		return DefaultCacheStatsInterval
	}

	result, err := time.ParseDuration(c.CacheStatsInterval)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse cache stats interval: %v\n", err)
		os.Exit(1)
	}

	return result
}

// ParseProjectCacheSize returns the size of the project cache, falling back to
// DefaultProjectCacheSize when the value is unset. A non-positive size is also
// defaulted, because the underlying LRU reads it as "unbounded" rather than as
// the misconfiguration it is; Validate rejects a negative value outright.
func (c *Config) ParseProjectCacheSize() int {
	if c.ProjectCacheSize <= 0 {
		return DefaultProjectCacheSize
	}

	return c.ProjectCacheSize
}

// ParseProjectCacheTTL returns the TTL duration for the project cache, falling
// back to DefaultProjectCacheTTL when the value is unset. Configs built in code
// may leave it empty, and neither an unset nor an invalid value terminates the
// process: an invalid one is returned as an error for the caller to surface.
func (c *Config) ParseProjectCacheTTL() (time.Duration, error) {
	if c.ProjectCacheTTL == "" {
		return DefaultProjectCacheTTL, nil
	}

	result, err := cache.ParseTTL(c.ProjectCacheTTL)
	if err != nil {
		return 0, fmt.Errorf("parse project cache TTL: %w", err)
	}

	return result, nil
}

// ParseMonitoringConfig returns the monitoring configuration for MongoDB query monitoring.
func (c *Config) ParseMonitoringConfig() *MonitorConfig {
	conf := &MonitorConfig{
		Enabled: c.MonitoringEnabled,
	}

	if c.MonitoringSlowQueryThreshold != "" {
		duration, err := time.ParseDuration(c.MonitoringSlowQueryThreshold)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse slow query threshold: %v\n", err)
			os.Exit(1)
		}
		conf.SlowQueryThreshold = duration
	}

	return conf
}
