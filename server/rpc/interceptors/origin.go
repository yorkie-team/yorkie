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
	"net/url"
	"path"
	"strings"
)

// matchOrigin reports whether origin is allowed by pattern.
//
// Supported forms:
//   - "*" matches any origin (universal wildcard).
//   - Otherwise pattern and origin must share scheme, host (label by
//     label) and port. Within a host label, "*" in the pattern matches
//     any sequence of characters other than ".".
//
// Path, query, and fragment must be empty on both sides; the Origin
// header never carries them.
func matchOrigin(pattern, origin string) bool {
	if pattern == "*" {
		return true
	}
	if pattern == "" || origin == "" {
		return false
	}
	if pattern == origin {
		return true
	}

	patternURL, err := url.Parse(pattern)
	if err != nil {
		return false
	}
	originURL, err := url.Parse(origin)
	if err != nil {
		return false
	}

	if !isPlainOrigin(patternURL) || !isPlainOrigin(originURL) {
		return false
	}

	if !strings.EqualFold(patternURL.Scheme, originURL.Scheme) {
		return false
	}
	if patternURL.Port() != originURL.Port() {
		return false
	}

	return matchHost(
		strings.ToLower(patternURL.Hostname()),
		strings.ToLower(originURL.Hostname()),
	)
}

// isPlainOrigin reports whether u is shaped like an Origin header:
// scheme and host present, no userinfo, no path/query/fragment.
func isPlainOrigin(u *url.URL) bool {
	if u.Scheme == "" || u.Host == "" {
		return false
	}
	if u.User != nil {
		return false
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return true
}

// matchHost matches host labels in lockstep. Each origin label must be
// non-empty; each pattern label is matched with path.Match, where "*"
// matches any sequence not containing "/" (and host labels cannot
// contain "/").
func matchHost(patternHost, originHost string) bool {
	patternLabels := strings.Split(patternHost, ".")
	originLabels := strings.Split(originHost, ".")
	if len(patternLabels) != len(originLabels) {
		return false
	}

	for i := range patternLabels {
		if originLabels[i] == "" {
			return false
		}
		ok, err := path.Match(patternLabels[i], originLabels[i])
		if err != nil || !ok {
			return false
		}
	}
	return true
}
