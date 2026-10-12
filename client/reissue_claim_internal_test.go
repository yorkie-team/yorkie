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

package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func TestClaimReissue(t *testing.T) {
	actorA, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	actorB, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	k1, k2 := key.Key("k1"), key.Key("k2")

	cli, err := New()
	require.NoError(t, err)

	assert.True(t, cli.claimReissue(k1, actorA))

	// A second never-synced document of k1 under the same actor restarts the
	// lamport at 1, so re-issuing it would mint the createdAt the first attach
	// already pushed.
	assert.False(t, cli.claimReissue(k1, actorA))

	// Another key, and k1 under another actor -- a reactivation that took a
	// new client id -- are unaffected.
	assert.True(t, cli.claimReissue(k2, actorA))
	assert.True(t, cli.claimReissue(k1, actorB))
	assert.False(t, cli.claimReissue(k1, actorB))
}
