/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

package types

// VersionInfo represents information of version.
type VersionInfo struct {
	// ClientVersion is the yorkie cli version.
	ClientVersion *VersionDetail `json:"clientVersion,omitempty" yaml:"clientVersion,omitempty"`

	// ServerVersion is the yorkie server version.
	ServerVersion *VersionDetail `json:"serverVersion,omitempty" yaml:"serverVersion,omitempty"`
}

// VersionDetail represents detail information of version.
type VersionDetail struct {
	// YorkieVersion
	YorkieVersion string `json:"yorkieVersion" yaml:"yorkieVersion"`

	// GoVersion
	GoVersion string `json:"goVersion" yaml:"goVersion"`

	// BuildDate
	BuildDate string `json:"buildDate" yaml:"buildDate"`
}
