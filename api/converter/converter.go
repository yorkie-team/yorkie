/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

// Package converter provides the converter for converting model to
// Protobuf, bytes and vice versa.
package converter

import "errors"

var (
	// ErrPackRequired is returned when an empty pack is passed.
	ErrPackRequired = errors.New("pack required")

	// ErrCheckpointRequired is returned when a pack with an empty checkpoint is
	// passed.
	ErrCheckpointRequired = errors.New("checkpoint required")

	// ErrUnsupportedOperation is returned when the given operation is not
	// supported yet.
	ErrUnsupportedOperation = errors.New("unsupported operation")

	// ErrUnsupportedElement is returned when the given element is not
	// supported yet.
	ErrUnsupportedElement = errors.New("unsupported element")

	// ErrUnsupportedEventType is returned when the given event type is not
	// supported yet.
	ErrUnsupportedEventType = errors.New("unsupported event type")

	// ErrUnsupportedValueType is returned when the given value type is not
	// supported yet.
	ErrUnsupportedValueType = errors.New("unsupported value type")

	// ErrUnsupportedCounterType is returned when the given counter type is not
	// supported yet.
	ErrUnsupportedCounterType = errors.New("unsupported counter type")
)
