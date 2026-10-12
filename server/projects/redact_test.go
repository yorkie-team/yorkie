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

package projects

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// The role hierarchy ranks a role it does not recognize as 0, which compares as
// more privileged than Owner, so the gate has to reject unknown roles itself.
// The database validates the roles it stores, but the comparison must not be
// the only thing standing between a corrupted row and the secret key.
func TestRedactForRole(t *testing.T) {
	tests := []struct {
		name    string
		role    database.MemberRole
		visible bool
	}{
		{"owner", database.Owner, true},
		{"admin", database.Admin, true},
		{"member", database.Member, false},
		{"empty", database.MemberRole(""), false},
		{"unknown", database.MemberRole("superuser"), false},
		{"mixed case admin", database.MemberRole("Admin"), false},
	}

	for _, tc := range tests {
		project := &types.Project{Name: "yorkie", SecretKey: "secret"}
		redacted := redactForRole(project, tc.role)

		if tc.visible {
			assert.Equal(t, "secret", redacted.SecretKey, tc.name)
		} else {
			assert.Empty(t, redacted.SecretKey, tc.name)
		}

		// Redaction returns a copy; the project it was handed keeps its key, so
		// a cached or shared project is not emptied for every later reader.
		assert.Equal(t, "secret", project.SecretKey, tc.name)
		assert.Equal(t, "yorkie", redacted.Name, tc.name)
	}
}
