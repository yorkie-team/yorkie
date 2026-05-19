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

package interceptors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchOrigin(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		origin  string
		want    bool
	}{
		{"universal star matches anything", "*", "https://foo.com", true},
		{"universal star matches http", "*", "http://localhost:3000", true},

		{"exact match", "https://foo.com", "https://foo.com", true},
		{"exact mismatch host", "https://foo.com", "https://bar.com", false},
		{"scheme mismatch", "https://foo.com", "http://foo.com", false},
		{"case-insensitive scheme", "HTTPS://foo.com", "https://foo.com", true},
		{"case-insensitive host", "https://FOO.com", "https://foo.com", true},

		{"leftmost label wildcard", "https://*.example.com", "https://api.example.com", true},
		{"leftmost label wildcard, empty does not match", "https://*.example.com", "https://.example.com", false},
		{"leftmost label wildcard rejects extra label", "https://*.example.com", "https://a.b.example.com", false},
		{"wildcard inside label, prefix", "https://*m.stock.example.com", "https://mini-m.stock.example.com", true},
		{"wildcard inside label, suffix", "https://api-*.example.com", "https://api-prod.example.com", true},
		{"wildcard inside label, middle", "https://a*z.example.com", "https://abcz.example.com", true},
		{
			"wildcard inside label, no match across dot",
			"https://*m.stock.example.com",
			"https://mini.m.stock.example.com",
			false,
		},

		{"port match", "https://foo.com:8080", "https://foo.com:8080", true},
		{"port mismatch", "https://foo.com:8080", "https://foo.com:9090", false},
		{"port present in pattern only", "https://foo.com:8080", "https://foo.com", false},
		{"port present in origin only", "https://foo.com", "https://foo.com:8080", false},

		{"path in origin is rejected", "https://foo.com", "https://foo.com/path", false},
		{"query in origin is rejected", "https://foo.com", "https://foo.com?x=1", false},

		{"empty pattern", "", "https://foo.com", false},
		{"empty origin", "https://foo.com", "", false},
		{"malformed pattern returns false", "https://[bad", "https://foo.com", false},
		{"malformed origin returns false", "https://foo.com", "https://[bad", false},

		{"label count mismatch", "https://*.example.com", "https://example.com", false},
		{"wildcard does not match empty label, suffix case", "https://api-*.example.com", "https://api-.example.com", true},

		{
			"wildcard with bracket characters falls back to no match",
			"https://[abc].example.com",
			"https://x.example.com",
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchOrigin(tc.pattern, tc.origin)
			assert.Equal(t, tc.want, got, "matchOrigin(%q, %q)", tc.pattern, tc.origin)
		})
	}
}
