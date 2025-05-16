/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package connecthelper_test

import (
	"net/http"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

var (
	canonicalKey = textproto.CanonicalMIMEHeaderKey(types.UserAgentKey)
)

func TestSDKTypeAndVersion(t *testing.T) {
	tests := []struct {
		name            string
		header          http.Header
		expectedType    string
		expectedVersion string
	}{
		{
			name: "yorkie-js/sdk",
			header: http.Header{
				canonicalKey: []string{"@yorkie-js/sdk/0.6.9"},
			},
			expectedType:    "@yorkie-js/sdk",
			expectedVersion: "0.6.9",
		},
		{
			name: "yorkie-js/react",
			header: http.Header{
				canonicalKey: []string{"@yorkie-js/react/0.6.9"},
			},
			expectedType:    "@yorkie-js/react",
			expectedVersion: "0.6.9",
		},
		{
			name: "dashboard",
			header: http.Header{
				canonicalKey: []string{"dashboard/0.6.9"},
			},
			expectedType:    "dashboard",
			expectedVersion: "0.6.9",
		},
		{
			name: "invalid user agent",
			header: http.Header{
				canonicalKey: []string{"invalid"},
			},
			expectedType:    "",
			expectedVersion: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(test.header)
			assert.Equal(t, test.expectedType, sdkType)
			assert.Equal(t, test.expectedVersion, sdkVersion)
		})
	}
}
