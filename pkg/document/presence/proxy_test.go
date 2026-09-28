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

package presence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestInitializeWithItsOwnData covers Initialize handed the very map the
// proxy already holds. Initialize empties that map in place before filling
// it, so without copying the incoming data first the wipe also empties the
// source and the presence comes out blank instead of unchanged.
func TestInitializeWithItsOwnData(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	data := presence.Data{"color": "red", "shape": "circle"}
	p := presence.New(ctx, data)

	p.Initialize(data)

	expected := presence.Data{"color": "red", "shape": "circle"}
	assert.Equal(t, expected, data)
	pc := ctx.ToChange().PresenceChange()
	require.NotNil(t, pc)
	assert.Equal(t, presence.Put, pc.ChangeType)
	assert.Equal(t, expected, pc.Presence)
}
