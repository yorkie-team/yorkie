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

package clients

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
)

// makeTestBackend builds a minimal *backend.Backend whose only purpose is to
// expose an in-memory database; VerifyChannelActor only touches the DB.
func makeTestBackend(t *testing.T) *backend.Backend {
	t.Helper()
	db, err := memory.New()
	if err != nil {
		t.Fatalf("memory.New: %v", err)
	}
	return &backend.Backend{DB: db}
}

func TestVerifyChannelActor_EphemeralActorAllowed(t *testing.T) {
	be := makeTestBackend(t)
	ctx := context.Background()

	// A freshly-allocated ID that has no row in the DB — channel-only
	// ephemeral actor. Must be accepted (no error, no info).
	refKey := types.ClientRefKey{
		ProjectID: types.ID("000000000000000000000001"),
		ClientID:  types.NewID(),
	}

	info, err := VerifyChannelActor(ctx, be, refKey)
	assert.NoError(t, err)
	assert.Nil(t, info)
}

func TestVerifyChannelActor_ActivatedClientAllowed(t *testing.T) {
	be := makeTestBackend(t)
	ctx := context.Background()
	projectID := types.ID("000000000000000000000001")

	cli, err := be.DB.ActivateClient(ctx, projectID, "test-key", nil)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}

	got, err := VerifyChannelActor(ctx, be, types.ClientRefKey{
		ProjectID: projectID,
		ClientID:  cli.ID,
	})
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, cli.ID, got.ID)
}

func TestVerifyChannelActor_DeactivatedClientRejected(t *testing.T) {
	be := makeTestBackend(t)
	ctx := context.Background()
	projectID := types.ID("000000000000000000000001")

	cli, err := be.DB.ActivateClient(ctx, projectID, "test-key", nil)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := be.DB.DeactivateClient(ctx, cli.RefKey()); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	_, err = VerifyChannelActor(ctx, be, types.ClientRefKey{
		ProjectID: projectID,
		ClientID:  cli.ID,
	})
	assert.Error(t, err)
	assert.True(
		t,
		errors.Is(err, database.ErrClientNotActivated),
		"expected ErrClientNotActivated, got %v", err,
	)
}
