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

package interceptors

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/server/logging"
)

// toRPCLogLevel determines the appropriate log level based on error type and connect code.
// This centralizes the logic for RPC error severity classification.
func toRPCLogLevel(err error) logging.Level {
	if err == nil {
		return logging.Debug
	}

	// Handle context cancellation as debug level (expected client behavior)
	if errors.Is(err, context.Canceled) {
		return logging.Debug
	}

	// Convert to connect error to get the code
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
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

// logRPC logs RPC calls with appropriate level based on error.
// If err is nil, logs success at debug level. Otherwise logs error with level based on error type.
func logRPC(logger *zap.SugaredLogger, procedure string, duration time.Duration, err error, isStream bool) {
	prefix := "RPC"
	if isStream {
		prefix = "RPC : stream"
	}

	if err == nil {
		logger.Debugf("%s : %q %s", prefix, procedure, duration)
	} else {
		level := toRPCLogLevel(err)
		template := "%s : %q %s => %q"

		switch level {
		case logging.Debug:
			logger.Debugf(template, prefix, procedure, duration, err)
		case logging.Info:
			logger.Infof(template, prefix, procedure, duration, err)
		case logging.Warn:
			logger.Warnf(template, prefix, procedure, duration, err)
		case logging.Error:
			logger.Errorf(template, prefix, procedure, duration, err)
		default:
			logger.Warnf(template, prefix, procedure, duration, err)
		}
	}
}

// logRPCError logs RPC errors (wrapper for logRPC)
func logRPCError(logger *zap.SugaredLogger, procedure string, duration time.Duration, err error) {
	logRPC(logger, procedure, duration, err, false)
}

// logRPCStreamError logs RPC stream errors (wrapper for logRPC)
func logRPCStreamError(logger *zap.SugaredLogger, procedure string, duration time.Duration, err error) {
	logRPC(logger, procedure, duration, err, true)
}

// logRPCStreamSuccess logs successful streaming RPC calls (wrapper for logRPC)
func logRPCStreamSuccess(logger *zap.SugaredLogger, procedure string, duration time.Duration) {
	logRPC(logger, procedure, duration, nil, true)
}
