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

package connecthelper

import (
	goerrors "errors"
	"fmt"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/errors"
)

func TestStatus(t *testing.T) {
	t.Run("errorToConnectCode test", func(t *testing.T) {
		// Test existing functionality
		connectErr := ToConnectError(converter.ErrPackRequired)
		assert.NotNil(t, connectErr)

		var cErr *connect.Error
		assert.ErrorAs(t, connectErr, &cErr)
		assert.Equal(t, connect.CodeInvalidArgument, cErr.Code())
	})
}

func TestNewErrorSystemIntegration(t *testing.T) {
	t.Run("ToStatusError with new error system", func(t *testing.T) {
		// Test that new error system is handled correctly
		originalErr := errors.NotFound("project not found")

		connectErr := ToConnectError(originalErr)
		assert.NotNil(t, connectErr)

		// Should be a connect error
		var cErr *connect.Error
		assert.ErrorAs(t, connectErr, &cErr)
		assert.Equal(t, connect.CodeNotFound, cErr.Code())
		assert.Contains(t, cErr.Message(), "project not found")
	})

	t.Run("ToStatusError with standard error", func(t *testing.T) {
		// Test that standard errors still work (fallback)
		standardErr := goerrors.New("some standard error")

		connectErr := ToConnectError(standardErr)
		assert.NotNil(t, connectErr)

		// Should default to internal error
		var cErr *connect.Error
		assert.ErrorAs(t, connectErr, &cErr)
		assert.Equal(t, connect.CodeInternal, cErr.Code())
	})

	t.Run("ToStatusError with wrapped new error", func(t *testing.T) {
		// Test that wrapped new errors are handled correctly
		baseErr := errors.InvalidArgument("invalid input")
		wrappedErr := fmt.Errorf("operation failed: %w", baseErr)

		connectErr := ToConnectError(wrappedErr)
		assert.NotNil(t, connectErr)

		var cErr *connect.Error
		assert.ErrorAs(t, connectErr, &cErr)
		assert.Equal(t, connect.CodeInvalidArgument, cErr.Code())
		assert.Contains(t, cErr.Message(), "operation failed")
	})

	t.Run("ErrorCode mapping consistency", func(t *testing.T) {
		// Test that all our error codes map to correct Connect codes
		tests := []struct {
			name         string
			statusErr    errors.StatusError
			expectedCode connect.Code
		}{
			{"NotFound", errors.NotFound("test"), connect.CodeNotFound},
			{"InvalidArgument", errors.InvalidArgument("test"), connect.CodeInvalidArgument},
			{"AlreadyExists", errors.AlreadyExists("test"), connect.CodeAlreadyExists},
			{"PermissionDenied", errors.PermissionDenied("test"), connect.CodePermissionDenied},
			{"FailedPrecondition", errors.FailedPrecond("test"), connect.CodeFailedPrecondition},
			{"Internal", errors.Internal("test"), connect.CodeInternal},
			{"Unavailable", errors.Unavailable("test"), connect.CodeUnavailable},
			{"Unauthenticated", errors.Unauthenticated("test"), connect.CodeUnauthenticated},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				connectErr := ToConnectError(tt.statusErr)
				assert.NotNil(t, connectErr)

				var cErr *connect.Error
				assert.ErrorAs(t, connectErr, &cErr)
				assert.Equal(t, tt.expectedCode, cErr.Code())
			})
		}
	})

	t.Run("ToStatusError with MetadataError integration", func(t *testing.T) {
		// Test that MetadataError is now handled by the unified system
		baseErr := errors.PermissionDenied("access denied")
		metadataErr := errors.WithMetadata(baseErr, map[string]string{
			"reason":   "unauthorized",
			"resource": "project-123",
		})

		connectErr := ToConnectError(metadataErr)
		assert.NotNil(t, connectErr)

		var cErr *connect.Error
		assert.ErrorAs(t, connectErr, &cErr)
		assert.Equal(t, connect.CodePermissionDenied, cErr.Code())

		// Check that metadata is preserved in error details
		hasErrorInfo := false
		for _, detail := range cErr.Details() {
			if msg, err := detail.Value(); err == nil {
				if errorInfo, ok := msg.(*errdetails.ErrorInfo); ok {
					hasErrorInfo = true
					assert.Equal(t, "unauthorized", errorInfo.Metadata["reason"])
					assert.Equal(t, "project-123", errorInfo.Metadata["resource"])
					break
				}
			}
		}
		assert.True(t, hasErrorInfo, "ErrorInfo should be present in connect error details")
	})

	t.Run("ToStatusError preserves custom metadata from WithMetadata", func(t *testing.T) {
		// Test that custom metadata is preserved through the integration
		baseErr := errors.NotFound("user not found")

		// Add multiple layers of metadata
		err1 := errors.WithMetadata(baseErr, map[string]string{
			"user_id": "123",
			"scope":   "read",
		})
		err2 := errors.WithMetadata(err1, map[string]string{
			"request_id": "req-456",
			"client_ip":  "192.168.1.1",
		})

		connectErr := ToConnectError(err2)
		assert.NotNil(t, connectErr)

		var cErr *connect.Error
		assert.ErrorAs(t, connectErr, &cErr)
		assert.Equal(t, connect.CodeNotFound, cErr.Code())

		// Verify all custom metadata is preserved
		foundMetadata := false
		for _, detail := range cErr.Details() {
			if msg, err := detail.Value(); err == nil {
				if errorInfo, ok := msg.(*errdetails.ErrorInfo); ok {
					foundMetadata = true
					// Check system metadata
					assert.Contains(t, errorInfo.Metadata, "code")

					// Check custom metadata from both WithMetadata calls
					assert.Equal(t, "123", errorInfo.Metadata["user_id"])
					assert.Equal(t, "read", errorInfo.Metadata["scope"])
					assert.Equal(t, "req-456", errorInfo.Metadata["request_id"])
					assert.Equal(t, "192.168.1.1", errorInfo.Metadata["client_ip"])
					break
				}
			}
		}
		assert.True(t, foundMetadata, "Custom metadata should be preserved in ErrorInfo")
	})
}

func TestErrorCodeToConnect(t *testing.T) {
	tests := []struct {
		name        string
		errorCode   errors.StatusCode
		connectCode connect.Code
	}{
		{"InvalidArgument", errors.ErrCodeInvalidArgument, connect.CodeInvalidArgument},
		{"NotFound", errors.ErrCodeNotFound, connect.CodeNotFound},
		{"AlreadyExists", errors.ErrCodeAlreadyExists, connect.CodeAlreadyExists},
		{"PermissionDenied", errors.ErrCodePermissionDenied, connect.CodePermissionDenied},
		{"ResourceExhausted", errors.ErrCodeResourceExhausted, connect.CodeResourceExhausted},
		{"FailedPrecondition", errors.ErrCodeFailedPrecondition, connect.CodeFailedPrecondition},
		{"Internal", errors.ErrCodeInternal, connect.CodeInternal},
		{"Unavailable", errors.ErrCodeUnavailable, connect.CodeUnavailable},
		{"Unauthenticated", errors.ErrCodeUnauthenticated, connect.CodeUnauthenticated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := connect.Code(tt.errorCode)
			assert.Equal(t, tt.connectCode, result)
		})
	}
}

func TestIsStatusError(t *testing.T) {
	t.Run("NewError", func(t *testing.T) {
		err := errors.NotFound("test")
		assert.True(t, isStatusError(err))
	})

	t.Run("StandardError", func(t *testing.T) {
		err := goerrors.New("standard error")
		assert.False(t, isStatusError(err))
	})

	t.Run("NilError", func(t *testing.T) {
		assert.False(t, isStatusError(nil))
	})
}

func TestErrorToConnectError(t *testing.T) {
	t.Run("NewError", func(t *testing.T) {
		statusErr := errors.AlreadyExists("resource exists")
		connectErr, ok := fromStatusError(statusErr)

		assert.True(t, ok)
		assert.NotNil(t, connectErr)
		assert.Equal(t, connect.CodeAlreadyExists, connectErr.Code())
		assert.Equal(t, "resource exists", connectErr.Message())
	})

	t.Run("StandardError", func(t *testing.T) {
		err := goerrors.New("standard error")
		connectErr, ok := fromStatusError(err)

		assert.False(t, ok)
		assert.Nil(t, connectErr)
	})

	t.Run("NilError", func(t *testing.T) {
		connectErr, ok := fromStatusError(nil)

		assert.False(t, ok)
		assert.Nil(t, connectErr)
	})
}

func TestToConnectErrorNewSystemPriority(t *testing.T) {
	t.Run("NewErrorPriority", func(t *testing.T) {
		// Test that new error system takes priority
		statusErr := errors.Internal("internal error")
		result := ToConnectError(statusErr)

		connectErr, ok := result.(*connect.Error)
		assert.True(t, ok)
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
		assert.Equal(t, "internal error", connectErr.Message())
	})

	t.Run("ClientErrorTypes", func(t *testing.T) {
		tests := []struct {
			name         string
			createError  func() error
			expectedCode connect.Code
		}{
			{"NotFound", func() error { return errors.NotFound("not found") }, connect.CodeNotFound},
			{"InvalidArgument", func() error { return errors.InvalidArgument("invalid") }, connect.CodeInvalidArgument},
			{"AlreadyExists", func() error { return errors.AlreadyExists("exists") }, connect.CodeAlreadyExists},
			{"PermissionDenied", func() error { return errors.PermissionDenied("denied") }, connect.CodePermissionDenied},
			{"ResourceExhausted", func() error { return errors.ResourceExhausted("exhausted") }, connect.CodeResourceExhausted},
			{"FailedPrecondition", func() error { return errors.FailedPrecond("failed") }, connect.CodeFailedPrecondition},
			{"Unauthenticated", func() error { return errors.Unauthenticated("unauth") }, connect.CodeUnauthenticated},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := tt.createError()
				result := ToConnectError(err)

				connectErr, ok := result.(*connect.Error)
				assert.True(t, ok)
				assert.Equal(t, tt.expectedCode, connectErr.Code())
			})
		}
	})

	t.Run("ServerErrorTypes", func(t *testing.T) {
		tests := []struct {
			name         string
			createError  func() error
			expectedCode connect.Code
		}{
			{"Internal", func() error { return errors.Internal("internal") }, connect.CodeInternal},
			{"Unavailable", func() error { return errors.Unavailable("unavailable") }, connect.CodeUnavailable},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := tt.createError()
				result := ToConnectError(err)

				connectErr, ok := result.(*connect.Error)
				assert.True(t, ok)
				assert.Equal(t, tt.expectedCode, connectErr.Code())
			})
		}
	})

	t.Run("WrappedNewErrors", func(t *testing.T) {
		// Test error chaining with new error system
		baseErr := errors.NotFound("base not found")
		wrappedErr := goerrors.New("wrapped: " + baseErr.Error())

		// Since the wrapped error doesn't implement StatusError,
		// it should fall back to legacy system
		result := ToConnectError(wrappedErr)

		connectErr, ok := result.(*connect.Error)
		assert.True(t, ok)
		// Should be treated as internal since it's not recognized by legacy system
		assert.Equal(t, connect.CodeInternal, connectErr.Code())
	})
}
