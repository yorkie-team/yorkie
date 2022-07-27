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

package grpchelper

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/runtime/protoiface"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/users"
)

// errorToCode maps an error to gRPC status code.
var errorToCode = map[error]codes.Code{
	// InvalidArgument means the request is malformed.
	converter.ErrPackRequired:       codes.InvalidArgument,
	converter.ErrCheckpointRequired: codes.InvalidArgument,
	time.ErrInvalidHexString:        codes.InvalidArgument,
	time.ErrInvalidActorID:          codes.InvalidArgument,
	types.ErrInvalidID:              codes.InvalidArgument,
	clients.ErrInvalidClientID:      codes.InvalidArgument,
	clients.ErrInvalidClientKey:     codes.InvalidArgument,
	types.ErrEmptyProjectFields:     codes.InvalidArgument,

	// NotFound means the requested resource does not exist.
	database.ErrProjectNotFound:  codes.NotFound,
	database.ErrClientNotFound:   codes.NotFound,
	database.ErrDocumentNotFound: codes.NotFound,
	database.ErrUserNotFound:     codes.NotFound,

	// AlreadyExists means the requested resource already exists.
	database.ErrProjectAlreadyExists:     codes.AlreadyExists,
	database.ErrProjectNameAlreadyExists: codes.AlreadyExists,

	// FailedPrecondition means the request is rejected because the state of the
	// system is not the desired state.
	database.ErrClientNotActivated:      codes.FailedPrecondition,
	database.ErrDocumentNotAttached:     codes.FailedPrecondition,
	database.ErrDocumentAlreadyAttached: codes.FailedPrecondition,
	packs.ErrInvalidServerSeq:           codes.FailedPrecondition,
	database.ErrConflictOnUpdate:        codes.FailedPrecondition,

	// Unimplemented means the server does not implement the functionality.
	converter.ErrUnsupportedOperation:   codes.Unimplemented,
	converter.ErrUnsupportedElement:     codes.Unimplemented,
	converter.ErrUnsupportedEventType:   codes.Unimplemented,
	converter.ErrUnsupportedValueType:   codes.Unimplemented,
	converter.ErrUnsupportedCounterType: codes.Unimplemented,

	// Unauthenticated means the request does not have valid authentication
	auth.ErrNotAllowed:           codes.Unauthenticated,
	auth.ErrUnexpectedStatusCode: codes.Unauthenticated,
	auth.ErrWebhookTimeout:       codes.Unauthenticated,
	users.ErrMismatchedPassword:  codes.Unauthenticated,
}

func detailsFromError(err error) (protoiface.MessageV1, bool) {
	invalidFieldsError, ok := err.(*types.InvalidFieldsError)
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

// ToStatusError returns a status.Error from the given logic error. If an error
// occurs while executing logic in API handler, gRPC status.error should be
// returned so that the client can know more about the status of the request.
func ToStatusError(err error) error {
	cause := errors.Unwrap(err)
	if cause == nil {
		cause = err
	}
	if code, ok := errorToCode[cause]; ok {
		return status.Error(code, err.Error())
	}

	// NOTE(hackerwins): InvalidFieldsError has details of invalid fields in
	// the error message.
	var invalidFieldsError *types.InvalidFieldsError
	if errors.As(err, &invalidFieldsError) {
		st := status.New(codes.InvalidArgument, err.Error())
		if details, ok := detailsFromError(err); ok {
			st, _ = st.WithDetails(details)
		}
		return st.Err()
	}

	return status.Error(codes.Internal, err.Error())
}
