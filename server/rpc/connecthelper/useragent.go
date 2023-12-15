/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package connecthelper

import (
	"net/http"
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
)

// SDKTypeAndVersion returns the type and version of the SDK from the given
// metadata.
func SDKTypeAndVersion(header http.Header) (string, string) {
	yorkieUserAgent := header.Get(types.UserAgentKey)
	if yorkieUserAgent == "" {
		return "", ""
	}

	split := strings.Split(yorkieUserAgent, "/")
	if len(split) != 2 {
		return "", ""
	}

	return split[0], split[1]
}
