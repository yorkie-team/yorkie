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

package messagebroker

import (
	"context"
)

// DummyBroker is a dummy broker that does nothing. It is used when the message
// broker is not configured.
type DummyBroker struct{}

// Produce does nothing.
func (mb *DummyBroker) Produce(_ context.Context, _ Message) error {
	return nil
}

// Close does nothing.
func (mb *DummyBroker) Close() error {
	return nil
}
