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

// StatusError represents an error that carries an error status.
// This interface allows for type-safe error handling with structured status codes.
type StatusError interface {
	error
	Status() StatusCode
	Code() string
	WithCode(code string) StatusError
}

// errorWithCode is the internal implementation of StatusError.
type errorWithStatus struct {
	err    error
	status StatusCode
	code   string
}

// Error returns the error message.
func (e errorWithStatus) Error() string {
	return e.err.Error()
}

// Status returns the error status.
func (e errorWithStatus) Status() StatusCode {
	return e.status
}

// Code returns the string representation of the error code.
func (e errorWithStatus) Code() string {
	return e.code
}

// Unwrap returns the underlying error for error chain compatibility.
func (e errorWithStatus) Unwrap() error {
	return e.err
}

// WithCode returns a new StatusError with the specified custom code.
func (e errorWithStatus) WithCode(code string) StatusError {
	return errorWithStatus{
		err:    e.err,
		status: e.status,
		code:   code,
	}
}

// newErrorWithStatus creates a new error with the specified status.
func newErrorWithStatus(err error, status StatusCode) StatusError {
	return errorWithStatus{
		err:    err,
		status: status,
		code:   "",
	}
}

// NotFound creates a new "not found" error.
// Use this when a requested resource does not exist.
func NotFound(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeNotFound)
}

// InvalidArgument creates a new "invalid argument" error.
// Use this when the client provides invalid input parameters.
func InvalidArgument(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeInvalidArgument)
}

// AlreadyExists creates a new "already exists" error.
// Use this when attempting to create a resource that already exists.
func AlreadyExists(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeAlreadyExists)
}

// PermissionDenied creates a new "permission denied" error.
// Use this when the caller lacks necessary permissions.
func PermissionDenied(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodePermissionDenied)
}

// ResourceExhausted creates a new "resource exhausted" error.
// Use this when quotas or rate limits are exceeded.
func ResourceExhausted(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeResourceExhausted)
}

// FailedPrecond creates a new "failed precondition" error.
// Use this when the system is not in the required state for the operation.
func FailedPrecond(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeFailedPrecondition)
}

// Unauthenticated creates a new "unauthenticated" error.
// Use this when authentication is required but not provided or invalid.
func Unauthenticated(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeUnauthenticated)
}

// Internal creates a new "internal" error.
// Use this for unexpected server-side failures.
func Internal(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeInternal)
}

// Unavailable creates a new "unavailable" error.
// Use this when the service is temporarily unavailable.
func Unavailable(message string) StatusError {
	return newErrorWithStatus(errors.New(message), ErrCodeUnavailable)
}

// StatusOf extracts the error status from an error.
// If the error implements StatusError, it returns the error's status.
// If the error wraps a StatusError, it unwraps and returns the wrapped error's status.
// If no status is found, it returns 0 to indicate no status is available.
func StatusOf(err error) StatusCode {
	if err == nil {
		return 0 // No error, no status
	}

	// Direct check for StatusError interface
	if statusErr, ok := err.(StatusError); ok {
		return statusErr.Status()
	}

	var statusErr StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Status()
	}

	// Return 0 for errors not from the new error system
	return 0
}

// IsStatus checks if the given error has the specified error status.
func IsStatus(err error, code StatusCode) bool {
	return StatusOf(err) == code
}

// IsClientError checks if the error represents a client-side error.
// Client errors typically indicate problems with the request or client state.
func IsClientError(err error) bool {
	status := StatusOf(err)
	return status.IsClientError()
}

// IsServerError checks if the error represents a server-side error.
// Server errors typically indicate problems with the server or infrastructure.
func IsServerError(err error) bool {
	status := StatusOf(err)
	return status.IsServerError()
}

// ErrorInfo provides detailed information about an error.
type ErrorInfo struct {
	Status       StatusCode
	Code         string
	Message      string
	IsClient     bool
	IsServer     bool
	StatusString string
	OriginalType string
}

// ErrorInfoOf extracts comprehensive information from an error.
// This is useful for logging and debugging purposes.
func ErrorInfoOf(err error) ErrorInfo {
	if err == nil {
		return ErrorInfo{
			Status:       0,
			Message:      "",
			IsClient:     false,
			IsServer:     false,
			StatusString: "",
			Code:         "",
			OriginalType: "",
		}
	}

	originalType := "StandardError"
	code := ""
	var statusErr StatusError
	if errors.As(err, &statusErr) {
		originalType = "StatusError"
		code = statusErr.Code()
	}

	status := StatusOf(err)

	return ErrorInfo{
		Status:       status,
		Message:      err.Error(),
		IsClient:     status.IsClientError(),
		IsServer:     status.IsServerError(),
		StatusString: status.String(),
		OriginalType: originalType,
		Code:         code,
	}
}
