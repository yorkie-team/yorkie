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

package mongo

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// TestNewProjectInfoDoc checks that the insert document carries every default
// the candidate sets. A field left out of the document decodes as the zero
// value on every later read, so the stored project would disagree with the
// info returned by the insert, as auto_revision_enabled once did.
func TestNewProjectInfoDoc(t *testing.T) {
	info := newProjectInfoCandidate("project", types.ID("000000000000000000000001"))
	doc := newProjectInfoDoc(info)

	value := reflect.ValueOf(*info)
	infoType := value.Type()

	tags := make(map[string]struct{}, infoType.NumField())
	for i := range infoType.NumField() {
		field := infoType.Field(i)
		tag := field.Tag.Get("bson")
		tags[tag] = struct{}{}

		// _id is left to MongoDB, and fields the candidate leaves zero read
		// back as zero whether they are stored or not.
		if tag == "_id" || value.Field(i).IsZero() {
			continue
		}

		stored, ok := doc[tag]
		if !assert.Truef(t, ok, "%s (%s) is missing from the project document", field.Name, tag) {
			continue
		}
		assert.Equalf(t, value.Field(i).Interface(), stored, "%s (%s) is stored with another value", field.Name, tag)
	}

	for key := range doc {
		_, ok := tags[key]
		assert.Truef(t, ok, "%s is not a field of ProjectInfo", key)
	}
}

// TestNewProjectInfoCandidateChannelSessionTTL checks that a new project is
// stored without a channel session TTL of its own, so that the server-wide
// ChannelSessionTTL the operator configured governs it until the project
// chooses a value through the admin API.
func TestNewProjectInfoCandidateChannelSessionTTL(t *testing.T) {
	info := newProjectInfoCandidate("project", types.ID("000000000000000000000001"))
	assert.Empty(t, info.ChannelSessionTTL)

	doc := newProjectInfoDoc(info)
	assert.Equal(t, "", doc["channel_session_ttl"])

	// The other defaults are untouched by the clearing.
	assert.Equal(t, database.DefaultClientDeactivateThreshold.String(), info.ClientDeactivateThreshold)
	assert.True(t, info.AutoRevisionEnabled)
}
