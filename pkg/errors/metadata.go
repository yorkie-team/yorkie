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

package errors

import (
	"errors"
)

// MetadataError represents an error with additional metadata.
// This provides similar functionality to metaerrors.MetaError but integrated
// with the new error system.
type MetadataError struct {
	err      error
	metadata map[string]string
}

// Error returns the error message.
func (e MetadataError) Error() string {
	return e.err.Error()
}

// Status returns the error code from the underlying error.
func (e MetadataError) Status() StatusCode {
	return StatusOf(e.err)
}

// Unwrap returns the underlying error for error chain compatibility.
func (e MetadataError) Unwrap() error {
	return e.err
}

// Metadata returns the metadata associated with the error.
func (e MetadataError) Metadata() map[string]string {
	// Return a copy to prevent external modification
	result := make(map[string]string)
	for k, v := range e.metadata {
		result[k] = v
	}
	return result
}

// WithMetadata wraps an error with additional metadata.
// This provides a way to add contextual information to errors.
func WithMetadata(err error, metadata map[string]string) error {
	if err == nil {
		return nil
	}

	// Return original error if metadata is empty
	if len(metadata) == 0 {
		return err
	}

	// If the error already has metadata, merge them
	existingMeta := Metadata(err)
	var finalMeta map[string]string

	if existingMeta != nil {
		// Merge existing metadata with new metadata
		finalMeta = make(map[string]string)
		for k, v := range existingMeta {
			finalMeta[k] = v
		}
		for k, v := range metadata {
			finalMeta[k] = v
		}

		// Get the underlying error without metadata wrapper
		if metaErr, ok := err.(MetadataError); ok {
			err = metaErr.err
		}
	} else {
		// Copy metadata to prevent external modification
		finalMeta = make(map[string]string)
		for k, v := range metadata {
			finalMeta[k] = v
		}
	}

	return MetadataError{
		err:      err,
		metadata: finalMeta,
	}
}

// Metadata extracts metadata from an error if it has any.
// Returns nil if the error doesn't have metadata.
func Metadata(err error) map[string]string {
	if err == nil {
		return nil
	}

	// Check if it's directly a MetadataError
	if metaErr, ok := err.(MetadataError); ok {
		return metaErr.Metadata()
	}

	// Check error chain for MetadataError
	var metaErr MetadataError
	if errors.As(err, &metaErr) {
		return metaErr.Metadata()
	}

	return nil
}
