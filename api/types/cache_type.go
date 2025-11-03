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

package types

// CacheType represents the type of cache.
type CacheType int

const (
	// CacheTypeUnknown represents an unknown cache type.
	CacheTypeUnknown CacheType = iota

	// CacheTypeProject represents the project cache type.
	CacheTypeProject

	// CacheTypeClient represents the client cache type.
	CacheTypeClient

	// CacheTypeDocument represents the document cache type.
	CacheTypeDocument
)

// String returns the string representation of the CacheType.
func (ct CacheType) String() string {
	switch ct {
	case CacheTypeProject:
		return "project"
	case CacheTypeClient:
		return "client"
	case CacheTypeDocument:
		return "document"
	default:
		return "unknown"
	}
}
