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

package logging

import (
	"context"
)

// loggerKey is the type used for the logger key in context.
type loggerKey struct{}

// With returns a new context with the provided logger.
func With(ctx context.Context, logger Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// From returns the logger stored in the provided context.
func From(ctx context.Context) Logger {
	if ctx == nil {
		return defaultLogger
	}

	logger, ok := ctx.Value(loggerKey{}).(Logger)
	if !ok {
		return defaultLogger
	}

	return logger
}
