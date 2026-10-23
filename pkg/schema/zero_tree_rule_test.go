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

package schema_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/schema"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestZeroTreeRule(t *testing.T) {
	ruleset := []types.Rule{{Path: "$", Type: "object"}, {Path: "$.zt", Type: "yorkie.ZeroTree"}}
	doc := document.New(helper.TestKey(t))
	content := map[string]any{"type": "content", "children": []any{
		map[string]any{"type": "paragraph", "children": []any{
			map[string]any{"type": "inline-text", "data": "ab"},
		}},
	}}

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewZeroTree("zt", content, nil)
		return nil
	}))
	assert.True(t, schema.ValidateYorkieRuleset(doc.RootObject(), ruleset).Valid)
	assert.Equal(t,
		`{"children":[{"children":[{"data":"ab","type":"inline-text"}],"type":"paragraph"}],"type":"content"}`,
		doc.RootObject().Get("zt").Marshal())

	assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewText("zt")
		return nil
	}))
	result := schema.ValidateYorkieRuleset(doc.RootObject(), ruleset)
	assert.False(t, result.Valid)
	assert.Equal(t, "expected yorkie.ZeroTree at path $.zt", result.Errors[0].Message)
}
