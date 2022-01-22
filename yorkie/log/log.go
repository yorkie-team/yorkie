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

package log

import (
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var defaultLogger *zap.SugaredLogger
var coreLogger zapcore.Core

var logLevel = zapcore.InfoLevel
var loggerOnce sync.Once

// Logger returns the default logger used by Yorkie.
func Logger() *zap.SugaredLogger {
	loggerOnce.Do(func() {
		initialize()
	})
	return defaultLogger
}

// Core returns the Core logger, where Core is a minial, fast logger interface.
func Core() zapcore.Core {
	loggerOnce.Do(func() {
		initialize()
	})
	return coreLogger
}

// SetLogLevel sets the level of global logger with ["debug", "info", "warn", "error", "panic", "fatal"].
// Loglevel must be sets before calling DefaultLogger() or CoreLogger() function.
func SetLogLevel(level string) {
	switch level {
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
	}
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

func initialize() {
	rawLogger := zap.New(zapcore.NewTee(
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(humanEncoderConfig()),
			zapcore.AddSync(os.Stdout),
			logLevel,
		),
	), zap.AddStacktrace(zap.ErrorLevel))
	defaultLogger = rawLogger.Sugar()
	coreLogger = rawLogger.Core()
}
