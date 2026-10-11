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

package server_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/test/helper"
)

// publicSecretKey is the signing key Yorkie once fell back to when
// Backend.SecretKey was unset. It is published in the source tree, so a token
// signed with it must never be accepted.
const publicSecretKey = "yorkie-secret" // #nosec G101

// testConfig returns a test config whose admin token signing key is unset.
func testConfig() *server.Config {
	conf := helper.TestConfig()
	conf.Backend.SecretKey = ""
	return conf
}

// newServer builds a server from conf without starting it and returns the
// admin token signing key it resolved.
func newServer(t *testing.T, conf *server.Config) string {
	t.Helper()

	// Keep the test off the network: the sample config names a MongoDB,
	// a Kafka and a StarRocks.
	conf.Mongo = nil
	conf.Kafka = nil
	conf.StarRocks = nil
	svr, err := server.New(conf)
	require.NoError(t, err)
	// The server never starts, so Shutdown fails to deregister a node that
	// was never registered; it still releases the backend.
	t.Cleanup(func() { _ = svr.Shutdown(true) })

	return conf.Backend.SecretKey
}

// forgeToken signs an admin token for username the way an attacker who knows
// key would.
func forgeToken(t *testing.T, key, username string) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.UserClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Username: username,
	})
	signed, err := token.SignedString([]byte(key))
	require.NoError(t, err)
	return signed
}

func TestAdminSecretKey(t *testing.T) {
	t.Run("unset key never resolves to a public key test", func(t *testing.T) {
		conf, err := server.NewConfigFromFile("config.sample.yml")
		require.NoError(t, err)
		assert.Empty(t, conf.Backend.SecretKey)

		key := newServer(t, conf)
		assert.NotEmpty(t, key)
		assert.NotEqual(t, publicSecretKey, key)

		// The token an attacker forges with the published key is refused.
		manager := auth.NewTokenManager(key, time.Hour)
		_, err = manager.Verify(forgeToken(t, publicSecretKey, "admin"))
		assert.Error(t, err)

		// A token the server issues itself still verifies.
		issued, err := manager.Generate("admin")
		require.NoError(t, err)
		claims, err := manager.Verify(issued)
		require.NoError(t, err)
		assert.Equal(t, "admin", claims.Username)
	})

	t.Run("unset key is generated per server test", func(t *testing.T) {
		first := newServer(t, testConfig())
		second := newServer(t, testConfig())
		assert.NotEmpty(t, first)
		assert.NotEmpty(t, second)
		assert.NotEqual(t, first, second)
	})

	t.Run("explicit key is kept test", func(t *testing.T) {
		conf := testConfig()
		conf.Backend.SecretKey = "cluster-wide-key"
		assert.Equal(t, "cluster-wide-key", newServer(t, conf))
	})
}
