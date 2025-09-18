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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorCode_String(t *testing.T) {
	tests := []struct {
		name string
		code StatusCode
		want string
	}{
		{"InvalidArgument", ErrCodeInvalidArgument, "invalid_argument"},
		{"NotFound", ErrCodeNotFound, "not_found"},
		{"AlreadyExists", ErrCodeAlreadyExists, "already_exists"},
		{"PermissionDenied", ErrCodePermissionDenied, "permission_denied"},
		{"ResourceExhausted", ErrCodeResourceExhausted, "resource_exhausted"},
		{"FailedPrecondition", ErrCodeFailedPrecondition, "failed_precondition"},
		{"Internal", ErrCodeInternal, "internal"},
		{"Unavailable", ErrCodeUnavailable, "unavailable"},
		{"Unauthenticated", ErrCodeUnauthenticated, "unauthenticated"},
		{"Unknown", StatusCode(999), "code_999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.code.String())
		})
	}
}

func TestErrorCode_IsClientError(t *testing.T) {
	clientCodes := []StatusCode{
		ErrCodeInvalidArgument,
		ErrCodeNotFound,
		ErrCodeAlreadyExists,
		ErrCodePermissionDenied,
		ErrCodeResourceExhausted,
		ErrCodeFailedPrecondition,
		ErrCodeUnauthenticated,
	}

	serverCodes := []StatusCode{
		ErrCodeInternal,
		ErrCodeUnavailable,
	}

	for _, code := range clientCodes {
		t.Run(fmt.Sprintf("ClientError_%s", code.String()), func(t *testing.T) {
			assert.True(t, code.IsClientError())
			assert.False(t, code.IsServerError())
		})
	}

	for _, code := range serverCodes {
		t.Run(fmt.Sprintf("ServerError_%s", code.String()), func(t *testing.T) {
			assert.False(t, code.IsClientError())
			assert.True(t, code.IsServerError())
		})
	}
}

func TestErrorConstructors(t *testing.T) {
	t.Run("NotFound", func(t *testing.T) {
		err := NotFound("resource not found")
		assert.Equal(t, "resource not found", err.Error())
		assert.Equal(t, ErrCodeNotFound, err.Status())
	})

	t.Run("InvalidArgument", func(t *testing.T) {
		err := InvalidArgument("invalid input")
		assert.Equal(t, "invalid input", err.Error())
		assert.Equal(t, ErrCodeInvalidArgument, err.Status())
	})

	t.Run("AlreadyExists", func(t *testing.T) {
		err := AlreadyExists("resource exists")
		assert.Equal(t, "resource exists", err.Error())
		assert.Equal(t, ErrCodeAlreadyExists, err.Status())
	})

	t.Run("Internal", func(t *testing.T) {
		err := Internal("server error")
		assert.Equal(t, "server error", err.Error())
		assert.Equal(t, ErrCodeInternal, err.Status())
	})
}

func TestStatusOf(t *testing.T) {
	t.Run("StatusError", func(t *testing.T) {
		err := NotFound("test error")
		code := StatusOf(err)
		assert.Equal(t, ErrCodeNotFound, code)
	})

	t.Run("WrappedStatusError", func(t *testing.T) {
		baseErr := NotFound("base error")
		wrappedErr := fmt.Errorf("wrapped: %w", baseErr)
		code := StatusOf(wrappedErr)
		assert.Equal(t, ErrCodeNotFound, code)
	})

	t.Run("StandardError", func(t *testing.T) {
		err := errors.New("standard error")
		assert.Equal(t, StatusCode(0), StatusOf(err))
	})

	t.Run("NilError", func(t *testing.T) {
		code := StatusOf(nil)
		assert.Equal(t, StatusCode(0), code)
	})
}

func TestIsStatus(t *testing.T) {
	err := NotFound("test error")

	assert.True(t, IsStatus(err, ErrCodeNotFound))
	assert.False(t, IsStatus(err, ErrCodeInvalidArgument))
	assert.False(t, IsStatus(nil, ErrCodeNotFound))
}

func TestErrorTypeChecking(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode StatusCode
		expected   bool
	}{
		{"IsNotFound_True", NotFound("test"), ErrCodeNotFound, true},
		{"IsNotFound_False", InvalidArgument("test"), ErrCodeNotFound, false},
		{"IsInvalidArgument_True", InvalidArgument("test"), ErrCodeInvalidArgument, true},
		{"IsInvalidArgument_False", NotFound("test"), ErrCodeInvalidArgument, false},
		{"IsAlreadyExists_True", AlreadyExists("test"), ErrCodeAlreadyExists, true},
		{"IsInternal_True", Internal("test"), ErrCodeInternal, true},
		{"IsUnavailable_True", Unavailable("test"), ErrCodeUnavailable, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsStatus(tt.err, tt.statusCode)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestErrorCategoryChecking(t *testing.T) {
	t.Run("ClientErrors", func(t *testing.T) {
		clientErrors := []StatusError{
			NotFound("test"),
			InvalidArgument("test"),
			AlreadyExists("test"),
			PermissionDenied("test"),
			ResourceExhausted("test"),
			FailedPrecond("test"),
			Unauthenticated("test"),
		}

		for _, err := range clientErrors {
			assert.True(t, IsClientError(err), "Expected %v to be a client error", err)
			assert.False(t, IsServerError(err), "Expected %v to not be a server error", err)
		}
	})

	t.Run("ServerErrors", func(t *testing.T) {
		statusErrors := []StatusError{
			Internal("test"),
			Unavailable("test"),
		}

		for _, err := range statusErrors {
			assert.False(t, IsClientError(err), "Expected %v to not be a client error", err)
			assert.True(t, IsServerError(err), "Expected %v to be a server error", err)
		}
	})
}

func TestGetErrorInfo(t *testing.T) {
	t.Run("StatusError", func(t *testing.T) {
		err := NotFound("test resource not found")
		info := ErrorInfoOf(err)

		assert.Equal(t, ErrCodeNotFound, info.Status)
		assert.Equal(t, "test resource not found", info.Message)
		assert.True(t, info.IsClient)
		assert.False(t, info.IsServer)
		assert.Equal(t, "not_found", info.StatusString)
		assert.Equal(t, "StatusError", info.OriginalType)
	})

	t.Run("StandardError", func(t *testing.T) {
		err := errors.New("standard error")
		info := ErrorInfoOf(err)

		assert.Equal(t, StatusCode(0), info.Status) // Standard errors don't have codes
		assert.Equal(t, "standard error", info.Message)
		assert.False(t, info.IsClient)
		assert.False(t, info.IsServer)
		assert.Equal(t, "code_0", info.StatusString) // Code 0 string representation
		assert.Equal(t, "StandardError", info.OriginalType)
	})

	t.Run("NilError", func(t *testing.T) {
		info := ErrorInfoOf(nil)
		assert.Equal(t, StatusCode(0), info.Status)
		assert.Equal(t, "", info.Message)
		assert.False(t, info.IsClient)
		assert.False(t, info.IsServer)
		assert.Equal(t, "", info.StatusString)
		assert.Equal(t, "", info.OriginalType)
	})
}

func TestErrorChaining(t *testing.T) {
	t.Run("WrappedStatusError", func(t *testing.T) {
		baseErr := NotFound("base error")
		wrappedErr := fmt.Errorf("operation failed: %w", baseErr)
		doubleWrappedErr := fmt.Errorf("request failed: %w", wrappedErr)
		assert.Equal(t, ErrCodeNotFound, StatusOf(doubleWrappedErr))
	})

	t.Run("UnwrapChain", func(t *testing.T) {
		baseErr := InvalidArgument("invalid input").WithCode("invalid_format")
		wrappedErr := fmt.Errorf("validation failed: %w", baseErr)

		// Test that we can unwrap to the original error
		var statusErr StatusError
		assert.True(t, errors.As(wrappedErr, &statusErr))
		assert.Equal(t, ErrCodeInvalidArgument, statusErr.Status())
		assert.Equal(t, "invalid input", statusErr.Error())
	})
}

func TestWithMetadata(t *testing.T) {
	t.Run("WithMetadata adds metadata to error", func(t *testing.T) {
		baseErr := NotFound("user not found")
		metadata := map[string]string{
			"user_id": "123",
			"scope":   "read",
		}

		errWithMeta := WithMetadata(baseErr, metadata)
		assert.NotNil(t, errWithMeta)

		// Should still preserve the original error code
		assert.Equal(t, ErrCodeNotFound, StatusOf(errWithMeta))

		// Should be able to extract metadata
		extractedMeta := Metadata(errWithMeta)
		assert.Equal(t, "123", extractedMeta["user_id"])
		assert.Equal(t, "read", extractedMeta["scope"])
	})

	t.Run("WithMetadata on nil error returns nil", func(t *testing.T) {
		result := WithMetadata(nil, map[string]string{"key": "value"})
		assert.Nil(t, result)
	})

	t.Run("WithMetadata with nil metadata returns original error", func(t *testing.T) {
		baseErr := Internal("internal error")
		result := WithMetadata(baseErr, nil)
		assert.Equal(t, baseErr, result)
	})

	t.Run("WithMetadata with empty metadata returns original error", func(t *testing.T) {
		baseErr := Internal("internal error")
		result := WithMetadata(baseErr, map[string]string{})
		assert.Equal(t, baseErr, result)
	})

	t.Run("Multiple WithMetadata calls merge metadata", func(t *testing.T) {
		baseErr := PermissionDenied("access denied")

		err1 := WithMetadata(baseErr, map[string]string{"reason": "unauthorized"})
		err2 := WithMetadata(err1, map[string]string{"resource": "user-123"})

		metadata := Metadata(err2)
		assert.Equal(t, "unauthorized", metadata["reason"])
		assert.Equal(t, "user-123", metadata["resource"])

		// Should still preserve the original error code
		assert.Equal(t, ErrCodePermissionDenied, StatusOf(err2))
	})

	t.Run("WithMetadata preserves error chain", func(t *testing.T) {
		baseErr := AlreadyExists("item exists")
		wrappedErr := fmt.Errorf("operation failed: %w", baseErr)
		errWithMeta := WithMetadata(wrappedErr, map[string]string{"item_id": "456"})

		// Should still be able to unwrap to the original error
		var statusErr StatusError
		assert.True(t, errors.As(errWithMeta, &statusErr))
		assert.Equal(t, ErrCodeAlreadyExists, statusErr.Status())

		// Should have metadata
		metadata := Metadata(errWithMeta)
		assert.Equal(t, "456", metadata["item_id"])
	})
}
