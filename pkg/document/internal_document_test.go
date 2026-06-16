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

package document_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestInternalDocumentResetPresences pins the contract used by the server
// to enforce disable_presence: ResetPresences must drop every cached
// presence entry so a subsequent serialization carries an empty map.
func TestInternalDocumentResetPresences(t *testing.T) {
	doc := document.New("test-doc")
	assert.NoError(t, doc.Update(func(_ *json.Object, p *presence.Presence) error {
		p.Set("name", "alice")
		return nil
	}))
	assert.NotEmpty(t, doc.AllPresences(), "expected presence to be populated before reset")

	doc.InternalDocument().ResetPresences()

	assert.Empty(t, doc.AllPresences(),
		"ResetPresences should clear the presence map and online-clients set")
}
