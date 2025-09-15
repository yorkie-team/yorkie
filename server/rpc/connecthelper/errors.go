/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package connecthelper provides helper functions for connectRPC.
package connecthelper

import (
	"context"
	goerrors "errors"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/yorkie-team/yorkie/internal/validation"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/logging"
)

// ToConnectError returns connect.Error from the given logic error. If an error
// occurs while executing logic in API handler, connectRPC connect.error should be
// returned so that the client can know more about the status of the request.
func ToConnectError(err error) error {
	if err == nil {
		return nil
	}

	if goerrors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, err)
	}
	if goerrors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	}

	if connectErr, ok := fromStatusError(err); ok {
		return connectErr
	}

	if connectErr, ok := fromFormError(err); ok {
		return connectErr
	}

	return connect.NewError(connect.CodeInternal, err)
}

// CodeOf returns a string representation of the given error.
func CodeOf(err error) string {
	if err == nil {
		return "ok"
	}

	if goerrors.Is(err, context.Canceled) {
		return connect.CodeCanceled.String()
	}
	if goerrors.Is(err, context.DeadlineExceeded) {
		return connect.CodeDeadlineExceeded.String()
	}

	if isStatusError(err) {
		return connect.Code(errors.StatusOf(err)).String()
	}

	return connect.CodeInternal.String()
}

// isStatusError checks if an error is from the new structured error system.
// It returns true only if the error (or any error in its chain) implements StatusError.
func isStatusError(err error) bool {
	if err == nil {
		return false
	}

	// Check error chain using errors.As
	var statusErr errors.StatusError
	return goerrors.As(err, &statusErr)
}

// fromStatusError returns connect.Error from errors.StatusError.
func fromStatusError(err error) (*connect.Error, bool) {
	if err == nil {
		return nil, false
	}

	if !isStatusError(err) {
		return nil, false
	}

	status := errors.StatusOf(err)
	if status == 0 {
		return nil, false
	}

	// Add error metadata as ErrorInfo detail
	connectErr := connect.NewError(connect.Code(status), err)
	info := errors.ErrorInfoOf(err)
	if info.Status != 0 {
		metadata := map[string]string{
			"code": info.Code,
		}

		// Add user-defined metadata from MetadataError if present
		if userMetadata := errors.Metadata(err); userMetadata != nil {
			for key, value := range userMetadata {
				metadata[key] = value
			}
		}

		info := &errdetails.ErrorInfo{
			Reason:   info.StatusString,
			Metadata: metadata,
		}

		// Add error detail if possible
		if detail, detailErr := connect.NewErrorDetail(info); detailErr == nil {
			connectErr.AddDetail(detail)
		}
	}
	return connectErr, true
}

// fromFormError returns connect.Error from validation.FormError.
func fromFormError(err error) (*connect.Error, bool) {
	var invalidFieldsError *validation.FormError
	if !goerrors.As(err, &invalidFieldsError) {
		return nil, false
	}

	connectErr := connect.NewError(connect.CodeInvalidArgument, err)
	badRequest, ok := badRequestFromError(err)
	if !ok {
		return connectErr, true
	}
	if detail, err := connect.NewErrorDetail(badRequest); err == nil {
		connectErr.AddDetail(detail)
	}

	return connectErr, true
}

// badRequestFromError creates BadRequest details from validation errors.
func badRequestFromError(err error) (*errdetails.BadRequest, bool) {
	var invalidFieldsError *validation.FormError
	if !goerrors.As(err, &invalidFieldsError) {
		return nil, false
	}

	violations := invalidFieldsError.Violations
	br := &errdetails.BadRequest{}
	for _, violation := range violations {
		v := &errdetails.BadRequest_FieldViolation{
			Field:       violation.Field,
			Description: violation.Description,
		}
		br.FieldViolations = append(br.FieldViolations, v)
	}

	return br, true
}

// LogLevelOf returns logging.Level corresponding to the given connect.Error.
func LogLevelOf(err error) logging.Level {
	if err == nil {
		return logging.Debug
	}

	// Handle context cancellation as debug level (expected client behavior)
	if goerrors.Is(err, context.Canceled) {
		return logging.Debug
	}

	// Convert to connect error to get the code
	if connectErr := new(connect.Error); goerrors.As(err, &connectErr) {
		switch connectErr.Code() {
		case connect.CodeCanceled:
			// Client canceled request - usually not an issue
			return logging.Debug
		case connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeAlreadyExists, connect.CodeFailedPrecondition:
			// Client-side errors - usually expected validation or business logic errors
			return logging.Info
		case connect.CodeUnimplemented:
			// Client called unsupported feature - not a server issue
			return logging.Info
		case connect.CodeUnauthenticated, connect.CodePermissionDenied:
			// Security-related errors - worth noting but not alarming
			return logging.Warn
		case connect.CodeResourceExhausted:
			// Rate limiting or resource issues - worth noting
			return logging.Warn
		case connect.CodeInternal, connect.CodeDataLoss, connect.CodeUnknown:
			// Server-side errors - need immediate attention
			return logging.Error
		case connect.CodeUnavailable, connect.CodeDeadlineExceeded:
			// Service availability issues - should be monitored
			return logging.Error
		default:
			// Unknown codes - treat as potential issues
			return logging.Warn
		}
	}

	// For non-connect errors, use warn as default
	return logging.Warn
}
