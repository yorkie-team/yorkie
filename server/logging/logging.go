/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package logging provides logging facilities for Yorkie Server.
package logging

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is a wrapper of zap.Logger.
type Logger = *zap.SugaredLogger

// Field is a wrapper of zap.Field.
type Field = zap.Field

var defaultLogger Logger
var logLevel = zapcore.InfoLevel
var loggerOnce sync.Once

// SetLogLevel sets the level of global logger with ["debug", "info", "warn", "error", "panic", "fatal"].
// SetLoglevel must be sets before calling DefaultLogger() or New().
func SetLogLevel(level string) error {
	switch strings.ToLower(level) {
	case "debug":
		logLevel = zapcore.DebugLevel
	case "info":
		logLevel = zapcore.InfoLevel
	case "warn":
		logLevel = zapcore.WarnLevel
	case "error":
		logLevel = zapcore.ErrorLevel
	case "panic":
		logLevel = zapcore.PanicLevel
	case "fatal":
		logLevel = zapcore.FatalLevel
	default:
		return fmt.Errorf("invalid log level: %s", level)
	}
	return nil
}

// New creates a new logger with the given configuration.
func New(name string, fields ...Field) Logger {
	logger := newLogger(name)

	if len(fields) > 0 {
		var args = make([]interface{}, len(fields))
		for i, field := range fields {
			args[i] = field
		}

		logger = logger.With(args...)
	}

	return logger
}

// NewField creates a new field with the given key and value.
func NewField(key string, value string) Field {
	return zap.String(key, value)

}

// DefaultLogger returns the default logger used by Yorkie.
func DefaultLogger() Logger {
	loggerOnce.Do(func() {
		defaultLogger = newLogger("default")
	})
	return defaultLogger
}

// Enabled returns true if the given level is enabled.
func Enabled(level zapcore.Level) bool {
	return level >= logLevel
}

// newLogger returns a new raw logger.
func newLogger(name string) Logger {
	return zap.New(zapcore.NewTee(
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(humanEncoderConfig()),
			zapcore.AddSync(os.Stdout),
			logLevel,
		),
	), zap.AddStacktrace(zap.ErrorLevel)).Named(name).Sugar()
}

func encoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "T",
		LevelKey:       "L",
		NameKey:        "N",
		CallerKey:      "C",
		MessageKey:     "M",
		StacktraceKey:  "S",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeTime:     zapcore.EpochTimeEncoder,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

func humanEncoderConfig() zapcore.EncoderConfig {
	cfg := encoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncodeDuration = zapcore.StringDurationEncoder
	return cfg
}
