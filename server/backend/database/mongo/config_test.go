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

package mongo_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
)

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		// 1. success
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}
		assert.NoError(t, config.Validate())

		// 2. invalid connection timeout
		config.ConnectionTimeout = "5"
		assert.Error(t, config.Validate())

		// 3. invalid ping timeout
		config.ConnectionTimeout = "5s"
		config.PingTimeout = "5"
		assert.Error(t, config.Validate())
	})

	t.Run("reject project cache TTL below cache.MinTTL", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}
		for _, ttl := range []string{"0s", "-1s", "1ns", "1ms", "99ms"} {
			config.ProjectCacheTTL = ttl
			assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-ttl", ttl)
		}

		config.ProjectCacheTTL = "100ms"
		assert.NoError(t, config.Validate())
	})

	t.Run("reject negative project cache size", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
			ProjectCacheSize:  -1,
		}
		assert.ErrorContains(t, config.Validate(), "--mongo-project-cache-size")

		// A non-positive size must never reach the LRU, which reads it as
		// "unbounded" instead of rejecting it.
		assert.Equal(t, mongo.DefaultProjectCacheSize, config.ParseProjectCacheSize())
	})

	t.Run("default caches for a config built in code", func(t *testing.T) {
		// A Config built in code may leave the cache fields empty, and an unset
		// value must resolve to the default instead of exiting.
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
		}
		assert.NoError(t, config.Validate())

		ttl, err := config.ParseProjectCacheTTL()
		assert.NoError(t, err)
		assert.Equal(t, mongo.DefaultProjectCacheTTL, ttl)
		assert.Equal(t, mongo.DefaultProjectCacheSize, config.ParseProjectCacheSize())
		assert.Equal(t, mongo.DefaultCacheStatsInterval, config.ParseCacheStatsInterval())
	})

	t.Run("report an invalid project cache TTL without exiting", func(t *testing.T) {
		config := &mongo.Config{
			ConnectionTimeout: "5s",
			PingTimeout:       "5s",
			ProjectCacheTTL:   "1ms",
		}
		_, err := config.ParseProjectCacheTTL()
		assert.ErrorIs(t, err, cache.ErrInvalidTTL)
	})

	t.Run("mask the password of the connection URI", func(t *testing.T) {
		// The redacted URI is logged at startup, so nothing that reaches a log
		// may carry the password, while the rest stays readable. Userinfo is
		// concatenated rather than written inline: gosec's G101 reads any
		// password inside a URL literal as a hardcoded credential, the masked
		// one included.
		const userinfo = "admin:s3cret@"
		const encodedUserinfo = "admin:s3%40cret@"
		const masked = "admin:xxxxx@"

		for _, tc := range []struct {
			uri      string
			redacted string
		}{{
			uri:      "mongodb://localhost:27017",
			redacted: "mongodb://localhost:27017",
		}, {
			uri:      "mongodb://" + userinfo + "mongos.default.svc.cluster.local:27017/?authSource=admin",
			redacted: "mongodb://" + masked + "mongos.default.svc.cluster.local:27017/?authSource=admin",
		}, {
			// A replica set names every host in the authority.
			uri:      "mongodb://" + userinfo + "h1:27017,h2:27017/yorkie?replicaSet=rs0",
			redacted: "mongodb://" + masked + "h1:27017,h2:27017/yorkie?replicaSet=rs0",
		}, {
			uri:      "mongodb+srv://" + encodedUserinfo + "cluster0.example.mongodb.net/yorkie",
			redacted: "mongodb+srv://" + masked + "cluster0.example.mongodb.net/yorkie",
		}} {
			config := &mongo.Config{ConnectionURI: tc.uri}
			assert.Equal(t, tc.redacted, config.RedactedConnectionURI())
		}
	})

	t.Run("mask the credential-bearing options of the connection URI", func(t *testing.T) {
		// The userinfo password is not the only credential a MongoDB
		// connection string carries: several options hold one in the option
		// string, and those reach the same log line. Names and values are
		// concatenated rather than written inline, because gosec's G101 reads
		// a credential-looking literal as a hardcoded credential.
		const value = "s3cret"
		const mask = "xxxxx"
		const prefix = "mongodb://h1:27017/yorkie?"

		for _, tc := range []struct {
			uri      string
			redacted string
		}{{
			// The client key passphrase.
			uri:      prefix + "tlsCertificateKeyFilePassword=" + value + "&replicaSet=rs0",
			redacted: prefix + "tlsCertificateKeyFilePassword=" + mask + "&replicaSet=rs0",
		}, {
			// The legacy alias of the option above.
			uri:      prefix + "sslClientCertificateKeyPassword=" + value,
			redacted: prefix + "sslClientCertificateKeyPassword=" + mask,
		}, {
			// The SOCKS5 proxy password.
			uri:      prefix + "proxyPassword=" + value + "&proxyHost=proxy.example",
			redacted: prefix + "proxyPassword=" + mask + "&proxyHost=proxy.example",
		}, {
			// The AWS session token, which names no secret in the option name.
			uri:      prefix + "authMechanismProperties=AWS_SESSION_TOKEN:" + value,
			redacted: prefix + "authMechanismProperties=" + mask,
		}, {
			// Option names are case-insensitive in the connection string, so
			// an unusual spelling must not slip the mask.
			uri:      prefix + "TLSCERTIFICATEKEYFILEPASSWORD=" + value,
			redacted: prefix + "TLSCERTIFICATEKEYFILEPASSWORD=" + mask,
		}, {
			// An option carrying no credential stays readable.
			uri:      prefix + "authSource=admin&replicaSet=rs0",
			redacted: prefix + "authSource=admin&replicaSet=rs0",
		}} {
			config := &mongo.Config{ConnectionURI: tc.uri}
			assert.Equal(t, tc.redacted, config.RedactedConnectionURI())
			assert.NotContains(t, config.RedactedConnectionURI(), value)
		}
	})

	t.Run("mask a password spelled with an unescaped delimiter", func(t *testing.T) {
		// A password is supposed to be percent-encoded, but one that is not
		// still reaches the log line. net/url cuts the fragment and the query
		// off before it looks for the authority, so an unescaped '#' or '?' in
		// the password leaves it with no userinfo to mask at all; the userinfo
		// is therefore located on the raw URI. Values are concatenated rather
		// than written inline, because gosec's G101 reads a URL literal
		// carrying a password as a hardcoded credential.
		const value = "s3cret"
		const masked = "admin:xxxxx@"

		for _, tc := range []struct {
			uri      string
			redacted string
		}{{
			// '#' would otherwise open a fragment that swallows the host.
			uri:      "mongodb://admin:" + value + "#x" + "@h1:27017",
			redacted: "mongodb://" + masked + "h1:27017",
		}, {
			// '?' would otherwise open the option string.
			uri:      "mongodb://admin:" + value + "?x" + "@h1:27017/yorkie",
			redacted: "mongodb://" + masked + "h1:27017/yorkie",
		}, {
			// The last '@' closes the userinfo, so an '@' inside the password
			// does not end it early.
			uri:      "mongodb://admin:" + value + "@x" + "@h1:27017",
			redacted: "mongodb://" + masked + "h1:27017",
		}, {
			// An '@' in an option value closes no userinfo.
			uri: "mongodb://admin:" + value + "@h1:27017/yorkie" +
				"?tlsCertificateKeyFilePassword=" + value + "@x",
			redacted: "mongodb://" + masked + "h1:27017/yorkie" +
				"?tlsCertificateKeyFilePassword=xxxxx",
		}, {
			// ';' is the legacy option separator, which the drivers accept.
			uri: "mongodb://h1:27017/yorkie?replicaSet=rs0" +
				";tlsCertificateKeyFilePassword=" + value,
			redacted: "mongodb://h1:27017/yorkie?replicaSet=rs0" +
				";tlsCertificateKeyFilePassword=xxxxx",
		}} {
			config := &mongo.Config{ConnectionURI: tc.uri}
			assert.Equal(t, tc.redacted, config.RedactedConnectionURI())
			assert.NotContains(t, config.RedactedConnectionURI(), value)
		}
	})

	t.Run("drop a connection URI whose credentials cannot be located", func(t *testing.T) {
		// Nothing of a URI this cannot take apart is reported: masking too
		// much costs a log line, masking too little publishes the password.
		const value = "s3cret"

		for _, uri := range []string{
			// A scheme-opaque URI has no authority to look in.
			"mongodb:admin:" + value + "@h1:27017",
			// An unescaped '/' in the password puts the '@' that closes the
			// userinfo after a character that ends the authority.
			"mongodb://admin:" + value + "/x" + "@h1:27017",
		} {
			config := &mongo.Config{ConnectionURI: uri}
			assert.Equal(t, "(unparsable URI)", config.RedactedConnectionURI())
		}
	})

	t.Run("drop a connection URI that does not parse", func(t *testing.T) {
		// The userinfo of an unparsable URI cannot be located, so none of the
		// URI is reported rather than risking the password in a log line.
		config := &mongo.Config{ConnectionURI: "://admin:s3cret@localhost:27017"}
		assert.NotContains(t, config.RedactedConnectionURI(), "s3cret")
		assert.NotContains(t, config.RedactedConnectionURI(), "admin")
	})

	t.Run("parse monitoring test", func(t *testing.T) {
		config := &mongo.Config{
			MonitoringEnabled:            true,
			MonitoringSlowQueryThreshold: "100ms",
		}
		monitorConfig := config.ParseMonitoringConfig()
		assert.Equal(t, true, monitorConfig.Enabled)
		assert.Equal(t, "100ms", monitorConfig.SlowQueryThreshold.String())
	})
}
