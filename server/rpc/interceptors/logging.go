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
	"time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

// logRPC logs RPC calls with appropriate level based on error.
// If err is nil, logs success at debug level. Otherwise logs error with level based on error type.
func logRPC(logger *zap.SugaredLogger, procedure string, duration time.Duration, err error, isStream bool) {
	prefix := "RPC"
	if isStream {
		prefix = "RPC : stream"
	}

	if err == nil {
		logger.Debugf("%s : %q %s", prefix, procedure, duration)
		return
	}

	template := "%s : %q %s => %q"
	switch connecthelper.LogLevelOf(err) {
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
