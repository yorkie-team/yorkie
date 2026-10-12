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

package project

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
)

// parseUpdateFlags returns the update command with the given options parsed.
func parseUpdateFlags(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := newUpdateCommandWithFlags()
	require.NoError(t, cmd.ParseFlags(args))
	return cmd
}

func TestUpdatableFieldsFromFlags(t *testing.T) {
	t.Run("sends only the options passed", func(t *testing.T) {
		// A project stored before ChannelSessionTTL existed reads it as
		// empty, which the server takes as its default but the validator
		// rejects. An update that does not touch it must not resend it.
		prj := &types.Project{
			ChannelSessionTTL:          "",
			AuthWebhookMinWaitInterval: "1s",
			ClientDeactivateThreshold:  "1h",
		}
		cmd := parseUpdateFlags(t, "--auth-webhook-url", "http://localhost:3000")

		fields := updatableFieldsFromFlags(cmd, prj)
		require.NoError(t, fields.Validate())
		assert.Equal(t, &types.UpdatableProjectFields{
			AuthWebhookURL: new("http://localhost:3000"),
		}, fields)
	})

	t.Run("sends options whose default is not zero only when passed", func(t *testing.T) {
		cmd := parseUpdateFlags(t,
			"--auth-webhook-request-timeout", "5s",
			"--channel-session-ttl", "30s",
		)

		fields := updatableFieldsFromFlags(cmd, &types.Project{})
		assert.Equal(t, new("5s"), fields.AuthWebhookRequestTimeout)
		assert.Equal(t, new("30s"), fields.ChannelSessionTTL)
		assert.Nil(t, fields.AuthWebhookMinWaitInterval)
		assert.Nil(t, fields.AuthWebhookMaxWaitInterval)
		assert.Nil(t, fields.ClientDeactivateThreshold)
		assert.Nil(t, fields.SnapshotThreshold)
	})

	t.Run("sends zero values that were passed explicitly", func(t *testing.T) {
		cmd := parseUpdateFlags(t,
			"--max-subscribers-per-document", "0",
			"--remove-on-detach=false",
			"--auth-webhook-url", "",
		)

		fields := updatableFieldsFromFlags(cmd, &types.Project{
			MaxSubscribersPerDocument: 10,
			RemoveOnDetach:            true,
			AuthWebhookURL:            "http://localhost:3000",
		})
		assert.Equal(t, new(0), fields.MaxSubscribersPerDocument)
		assert.Equal(t, new(false), fields.RemoveOnDetach)
		assert.Equal(t, new(""), fields.AuthWebhookURL)
	})

	t.Run("ALL adds every non-deprecated auth method", func(t *testing.T) {
		cmd := parseUpdateFlags(t, "--auth-webhook-method-add", "ALL")

		fields := updatableFieldsFromFlags(cmd, &types.Project{})
		require.NotNil(t, fields.AuthWebhookMethods)
		require.NoError(t, fields.Validate())

		methods := *fields.AuthWebhookMethods
		for _, m := range types.AuthMethods() {
			if m.IsDeprecated() {
				assert.NotContains(t, methods, string(m))
			} else {
				assert.Contains(t, methods, string(m))
			}
		}
		assert.Contains(t, methods, string(types.CreateRevision))
		assert.Contains(t, methods, string(types.AttachChannel))
	})

	t.Run("ALL removes every method, deprecated aliases included", func(t *testing.T) {
		cmd := parseUpdateFlags(t, "--auth-webhook-method-rm", "ALL")

		fields := updatableFieldsFromFlags(cmd, &types.Project{
			AuthWebhookMethods: []string{string(types.WatchDocument), string(types.PushPull)},
		})
		require.NotNil(t, fields.AuthWebhookMethods)
		assert.Empty(t, *fields.AuthWebhookMethods)
	})

	t.Run("method changes start from the stored methods", func(t *testing.T) {
		cmd := parseUpdateFlags(t,
			"--auth-webhook-method-add", string(types.RemoveDocument),
			"--auth-webhook-method-rm", string(types.PushPull),
		)

		fields := updatableFieldsFromFlags(cmd, &types.Project{
			AuthWebhookMethods: []string{string(types.PushPull), string(types.AttachDocument)},
		})
		assert.Equal(t, &[]string{
			string(types.AttachDocument),
			string(types.RemoveDocument),
		}, fields.AuthWebhookMethods)
	})
}

func TestHasUpdateFlag(t *testing.T) {
	assert.False(t, hasUpdateFlag(parseUpdateFlags(t)))
	assert.True(t, hasUpdateFlag(parseUpdateFlags(t, "--name", "renamed")))
}
