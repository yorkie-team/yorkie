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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// A TreeEdit content group always holds at least the root of one content node,
// so an empty or absent one is malformed. It has to be rejected at the wire
// boundary: FromTreeNodes reports an empty group as a nil root, and
// TreeEdit.Execute dereferences each content to deep-copy it, so carrying the
// nil into the operation is a nil-pointer panic on apply.
func TestTreeEditRejectsMissingContent(t *testing.T) {
	nodes, err := converter.FromTreeNodesWhenEdit([]*api.TreeNodes{{}})
	assert.Error(t, err)
	assert.Nil(t, nodes)

	nodes, err = converter.FromTreeNodesWhenEdit([]*api.TreeNodes{nil})
	assert.Error(t, err)
	assert.Nil(t, nodes)
}
