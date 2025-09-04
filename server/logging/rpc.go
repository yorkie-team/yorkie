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
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"
)

// RPCLogLevel represents the severity level for RPC logging
type RPCLogLevel int

const (
	RPCLogDebug RPCLogLevel = iota
	RPCLogInfo
	RPCLogWarn
	RPCLogError
)

// String returns the string representation of RPCLogLevel
func (l RPCLogLevel) String() string {
	switch l {
	case RPCLogDebug:
		return "debug"
	case RPCLogInfo:
		return "info"
	case RPCLogError:
		return "error"
	}
	return "warn"
}

// toRPCLogLevel determines the appropriate log level based on error type and connect code.
// This centralizes the logic for RPC error severity classification.
func toRPCLogLevel(err error) RPCLogLevel {
	if err == nil {
		return RPCLogDebug
	}

	// Handle context cancellation as debug level (expected client behavior)
	if errors.Is(err, context.Canceled) {
		return RPCLogDebug
	}

	// Convert to connect error to get the code
	if connectErr := new(connect.Error); errors.As(err, &connectErr) {
		switch connectErr.Code() {
		case connect.CodeCanceled:
			// Client canceled request - usually not an issue
			return RPCLogDebug
		case connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeAlreadyExists:
			// Client-side errors - usually expected validation errors
			return RPCLogInfo
		case connect.CodeUnauthenticated, connect.CodePermissionDenied:
			// Security-related errors - worth noting but not alarming
			return RPCLogWarn
		case connect.CodeFailedPrecondition, connect.CodeUnimplemented:
			// Business logic errors - should be investigated
			return RPCLogWarn
		case connect.CodeInternal, connect.CodeDataLoss, connect.CodeUnknown:
			// Server-side errors - need immediate attention
			return RPCLogError
		case connect.CodeUnavailable, connect.CodeDeadlineExceeded:
			// Service availability issues - should be monitored
			return RPCLogError
		case connect.CodeResourceExhausted:
			// Rate limiting or resource issues - worth noting
			return RPCLogWarn
		default:
			// Unknown codes - treat as potential issues
			return RPCLogWarn
		}
	}

	// For non-connect errors, use warn as default
	return RPCLogWarn
}

// logRPCErrorWithLevel logs RPC errors with the given message template.
// This is the core logging function used by both regular and stream RPC error logging.
func logRPCErrorWithLevel(
	logger *zap.SugaredLogger,
	template string,
	procedure string,
	duration time.Duration,
	err error,
) {
	switch toRPCLogLevel(err) {
	case RPCLogDebug:
		logger.Debugf(template, procedure, duration, err)
	case RPCLogInfo:
		logger.Infof(template, procedure, duration, err)
	case RPCLogWarn:
		logger.Warnf(template, procedure, duration, err)
	case RPCLogError:
		logger.Errorf(template, procedure, duration, err)
	default:
		logger.Warnf(template, procedure, duration, err)
	}
}

// LogRPCError logs RPC errors with the appropriate level based on error type.
// This provides a consistent way to log RPC errors across all interceptors.
func LogRPCError(logger *zap.SugaredLogger, procedure string, duration time.Duration, err error) {
	logRPCErrorWithLevel(logger, "RPC : %q %s => %q", procedure, duration, err)
}

// LogRPCStreamError logs RPC stream errors with the appropriate level.
// This is specifically for streaming RPC error logging.
func LogRPCStreamError(logger *zap.SugaredLogger, procedure string, duration time.Duration, err error) {
	logRPCErrorWithLevel(logger, "RPC : stream %q %s => %q", procedure, duration, err)
}

// logRPCSuccess logs successful RPC calls with the given message template.
// This is the core logging function used by both regular and stream RPC success logging.
func logRPCSuccess(logger *zap.SugaredLogger, messageTemplate string, procedure string, duration time.Duration) {
	logger.Debugf(messageTemplate, procedure, duration)
}

// LogRPCSuccess logs successful RPC calls at debug level.
func LogRPCSuccess(logger *zap.SugaredLogger, procedure string, duration time.Duration) {
	logRPCSuccess(logger, "RPC : %q %s", procedure, duration)
}

// LogRPCStreamSuccess logs successful streaming RPC calls at debug level.
func LogRPCStreamSuccess(logger *zap.SugaredLogger, procedure string, duration time.Duration) {
	logRPCSuccess(logger, "RPC : stream %q %s", procedure, duration)
}
