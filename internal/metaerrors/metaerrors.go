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

// Package metaerrors provides a way to attach metadata to errors.
package metaerrors

import "strings"

// MetaError is an error that can have metadata attached to it. This can be used
// to send additional information to the SDK or to the user.
type MetaError struct {
	// Err is the underlying error.
	Err error

	// Metadata is a map of additional information that can be attached to the
	// error.
	Metadata map[string]string
}

// New returns a new MetaError with the given error and metadata.
func New(err error, metadata map[string]string) *MetaError {
	return &MetaError{
		Err:      err,
		Metadata: metadata,
	}
}

// Error returns the error message.
func (e MetaError) Error() string {
	if len(e.Metadata) == 0 {
		return e.Err.Error()
	}

	sb := strings.Builder{}

	for key, val := range e.Metadata {
		if sb.Len() > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(key)
		sb.WriteString("=")
		sb.WriteString(val)
	}

	return e.Err.Error() + " [" + sb.String() + "]"
}
