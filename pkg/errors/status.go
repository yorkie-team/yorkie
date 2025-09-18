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

// Package errors provides server-side error management with structured error codes
// and consistent logging for RPC and background operations.
package errors

import "fmt"

// StatusCode represents the error codes used throughout the server.
// These codes are designed to be compatible with Connect protocol codes
// for seamless gRPC/Connect integration.
type StatusCode int

const (
	// ErrCodeInvalidArgument indicates that the client specified an invalid argument.
	// This indicates arguments that are problematic regardless of the state of the system.
	ErrCodeInvalidArgument StatusCode = 3

	// ErrCodeNotFound indicates that some requested entity (e.g., file or directory) was not found.
	ErrCodeNotFound StatusCode = 5

	// ErrCodeAlreadyExists indicates that the entity that a client attempted to create already exists.
	ErrCodeAlreadyExists StatusCode = 6

	// ErrCodePermissionDenied indicates that the caller does not have permission to execute the specified operation.
	ErrCodePermissionDenied StatusCode = 7

	// ErrCodeResourceExhausted indicates that some resource has been exhausted, perhaps a per-user quota.
	ErrCodeResourceExhausted StatusCode = 8

	// ErrCodeFailedPrecondition indicates that the operation was rejected because the system is not
	// in a state required for the operation's execution.
	ErrCodeFailedPrecondition StatusCode = 9

	// ErrCodeInternal indicates that some invariants expected by the underlying system have been broken.
	// This error code is reserved for serious errors.
	ErrCodeInternal StatusCode = 13

	// ErrCodeUnavailable indicates that the service is currently unavailable.
	// This is usually temporary, so clients can back off and retry idempotent operations.
	ErrCodeUnavailable StatusCode = 14

	// ErrCodeUnauthenticated indicates that the request does not have valid authentication credentials.
	ErrCodeUnauthenticated StatusCode = 16
)

// String returns the string representation of the error code.
// This matches the Connect protocol's string representation for consistency.
func (c StatusCode) String() string {
	switch c {
	case ErrCodeInvalidArgument:
		return "invalid_argument"
	case ErrCodeNotFound:
		return "not_found"
	case ErrCodeAlreadyExists:
		return "already_exists"
	case ErrCodePermissionDenied:
		return "permission_denied"
	case ErrCodeResourceExhausted:
		return "resource_exhausted"
	case ErrCodeFailedPrecondition:
		return "failed_precondition"
	case ErrCodeInternal:
		return "internal"
	case ErrCodeUnavailable:
		return "unavailable"
	case ErrCodeUnauthenticated:
		return "unauthenticated"
	default:
		return fmt.Sprintf("code_%d", int(c))
	}
}

// IsClientError returns true if the error code represents a client-side error.
func (c StatusCode) IsClientError() bool {
	switch c {
	case ErrCodeInvalidArgument, ErrCodeNotFound, ErrCodeAlreadyExists,
		ErrCodePermissionDenied, ErrCodeResourceExhausted, ErrCodeFailedPrecondition,
		ErrCodeUnauthenticated:
		return true
	default:
		return false
	}
}

// IsServerError returns true if the error code represents a server-side error.
func (c StatusCode) IsServerError() bool {
	switch c {
	case ErrCodeInternal, ErrCodeUnavailable:
		return true
	default:
		return false
	}
}
