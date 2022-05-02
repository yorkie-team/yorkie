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
