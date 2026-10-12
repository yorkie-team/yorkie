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

package database_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestMemberRoleIsAtLeast(t *testing.T) {
	tests := []struct {
		role     database.MemberRole
		required database.MemberRole
		expected bool
	}{
		{database.Owner, database.Owner, true},
		{database.Owner, database.Admin, true},
		{database.Owner, database.Member, true},
		{database.Admin, database.Owner, false},
		{database.Admin, database.Admin, true},
		{database.Admin, database.Member, true},
		{database.Member, database.Owner, false},
		{database.Member, database.Admin, false},
		{database.Member, database.Member, true},

		// A role outside the hierarchy carries no permission, whichever side
		// of the comparison it appears on.
		{database.MemberRole(""), database.Owner, false},
		{database.MemberRole(""), database.Admin, false},
		{database.MemberRole(""), database.Member, false},
		{database.MemberRole("superuser"), database.Owner, false},
		{database.MemberRole("superuser"), database.Admin, false},
		{database.MemberRole("superuser"), database.Member, false},
		{database.Owner, database.MemberRole(""), false},
		{database.Owner, database.MemberRole("superuser"), false},
		{database.Member, database.MemberRole(""), false},
	}

	for _, test := range tests {
		t.Run(fmt.Sprintf("%q is at least %q test", test.role, test.required), func(t *testing.T) {
			assert.Equal(t, test.expected, test.role.IsAtLeast(test.required))
		})
	}
}

func TestMemberRoleValidate(t *testing.T) {
	for _, role := range []database.MemberRole{database.Owner, database.Admin, database.Member} {
		assert.NoError(t, role.Validate())
	}

	for _, role := range []database.MemberRole{"", "superuser", "OWNER"} {
		assert.ErrorIs(t, role.Validate(), database.ErrInvalidMemberRole)
	}
}
