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

package logging

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
)

func TestGetRPCLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected RPCLogLevel
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: RPCLogDebug,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: RPCLogDebug,
		},
		{
			name:     "connect canceled",
			err:      connect.NewError(connect.CodeCanceled, errors.New("canceled")),
			expected: RPCLogDebug,
		},
		{
			name:     "invalid argument",
			err:      connect.NewError(connect.CodeInvalidArgument, errors.New("invalid")),
			expected: RPCLogInfo,
		},
		{
			name:     "not found",
			err:      connect.NewError(connect.CodeNotFound, errors.New("not found")),
			expected: RPCLogInfo,
		},
		{
			name:     "unauthenticated",
			err:      connect.NewError(connect.CodeUnauthenticated, errors.New("auth failed")),
			expected: RPCLogWarn,
		},
		{
			name:     "permission denied",
			err:      connect.NewError(connect.CodePermissionDenied, errors.New("no permission")),
			expected: RPCLogWarn,
		},
		{
			name:     "internal error",
			err:      connect.NewError(connect.CodeInternal, errors.New("internal")),
			expected: RPCLogError,
		},
		{
			name:     "unavailable",
			err:      connect.NewError(connect.CodeUnavailable, errors.New("unavailable")),
			expected: RPCLogError,
		},
		{
			name:     "resource exhausted",
			err:      connect.NewError(connect.CodeResourceExhausted, errors.New("exhausted")),
			expected: RPCLogWarn,
		},
		{
			name:     "unknown connect code",
			err:      connect.NewError(connect.CodeUnknown, errors.New("unknown")),
			expected: RPCLogError,
		},
		{
			name:     "non-connect error",
			err:      errors.New("regular error"),
			expected: RPCLogWarn,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level := toRPCLogLevel(tt.err)
			assert.Equal(t, tt.expected, level)
		})
	}
}

func TestRPCLogLevel_String(t *testing.T) {
	tests := []struct {
		level    RPCLogLevel
		expected string
	}{
		{RPCLogDebug, "debug"},
		{RPCLogInfo, "info"},
		{RPCLogWarn, "warn"},
		{RPCLogError, "error"},
		{RPCLogLevel(999), "warn"}, // unknown level defaults to warn
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.String())
		})
	}
}
