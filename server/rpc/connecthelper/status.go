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
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/validation"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/documents"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// errorToCode maps an error to connectRPC status code.
var errorToCode = map[error]connect.Code{
	// InvalidArgument means the request is malformed.
	converter.ErrPackRequired:       connect.CodeInvalidArgument,
	converter.ErrCheckpointRequired: connect.CodeInvalidArgument,
	time.ErrInvalidHexString:        connect.CodeInvalidArgument,
	time.ErrInvalidActorID:          connect.CodeInvalidArgument,
	types.ErrInvalidID:              connect.CodeInvalidArgument,
	clients.ErrInvalidClientID:      connect.CodeInvalidArgument,
	clients.ErrInvalidClientKey:     connect.CodeInvalidArgument,
	key.ErrInvalidKey:               connect.CodeInvalidArgument,
	types.ErrEmptyProjectFields:     connect.CodeInvalidArgument,

	// NotFound means the requested resource does not exist.
	database.ErrProjectNotFound:  connect.CodeNotFound,
	database.ErrClientNotFound:   connect.CodeNotFound,
	database.ErrDocumentNotFound: connect.CodeNotFound,
	database.ErrUserNotFound:     connect.CodeNotFound,

	// AlreadyExists means the requested resource already exists.
	database.ErrProjectAlreadyExists:     connect.CodeAlreadyExists,
	database.ErrProjectNameAlreadyExists: connect.CodeAlreadyExists,
	database.ErrUserAlreadyExists:        connect.CodeAlreadyExists,

	// FailedPrecondition means the request is rejected because the state of the
	// system is not the desired state.
	database.ErrClientNotActivated:      connect.CodeFailedPrecondition,
	database.ErrDocumentNotAttached:     connect.CodeFailedPrecondition,
	database.ErrDocumentAlreadyAttached: connect.CodeFailedPrecondition,
	database.ErrDocumentAlreadyDetached: connect.CodeFailedPrecondition,
	documents.ErrDocumentAttached:       connect.CodeFailedPrecondition,
	packs.ErrInvalidServerSeq:           connect.CodeFailedPrecondition,
	database.ErrConflictOnUpdate:        connect.CodeFailedPrecondition,

	// Unimplemented means the server does not implement the functionality.
	converter.ErrUnsupportedOperation:   connect.CodeUnimplemented,
	converter.ErrUnsupportedElement:     connect.CodeUnimplemented,
	converter.ErrUnsupportedEventType:   connect.CodeUnimplemented,
	converter.ErrUnsupportedValueType:   connect.CodeUnimplemented,
	converter.ErrUnsupportedCounterType: connect.CodeUnimplemented,

	// Unauthenticated means the request does not have valid authentication
	auth.ErrNotAllowed:             connect.CodeUnauthenticated,
	auth.ErrUnexpectedStatusCode:   connect.CodeUnauthenticated,
	auth.ErrWebhookTimeout:         connect.CodeUnauthenticated,
	database.ErrMismatchedPassword: connect.CodeUnauthenticated,

	// Canceled means the operation was canceled (typically by the caller).
	context.Canceled: connect.CodeCanceled,
}

func detailsFromError(err error) (*errdetails.BadRequest, bool) {
	invalidFieldsError, ok := err.(*validation.StructError)
	if !ok {
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

// ToStatusError returns a connect.Error from the given logic error. If an error
// occurs while executing logic in API handler, connectRPC connect.error should be
// returned so that the client can know more about the status of the request.
func ToStatusError(err error) error {
	cause := err
	for errors.Unwrap(cause) != nil {
		cause = errors.Unwrap(cause)
	}
	if code, ok := errorToCode[cause]; ok {
		return connect.NewError(code, err)
	}

	// NOTE(hackerwins): InvalidFieldsError has details of invalid fields in
	// the error message.
	var invalidFieldsError *validation.StructError
	if errors.As(err, &invalidFieldsError) {
		st := connect.NewError(connect.CodeInvalidArgument, err)
		details, ok := detailsFromError(err)
		if !ok {
			return st
		}
		if detail, err := connect.NewErrorDetail(details); err == nil {
			st.AddDetail(detail)
		}
		return st
	}

	if err := connect.NewError(connect.CodeInternal, err); err != nil {
		return fmt.Errorf("create status error: %w", err)
	}

	return nil
}

// ToRPCCodeString returns a string representation of the given error.
func ToRPCCodeString(err error) string {
	if err == nil {
		return "ok"
	}

	cause := err
	for errors.Unwrap(cause) != nil {
		cause = errors.Unwrap(cause)
	}
	if code, ok := errorToCode[cause]; ok {
		return code.String()
	}

	return connect.CodeInternal.String()
}
