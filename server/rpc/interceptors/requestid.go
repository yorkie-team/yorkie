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

package interceptors

import (
	"strconv"
	"sync/atomic"
)

// requestID is used to generate a unique request ID.
type requestID struct {
	prefix string
	id     int32
}

// newRequestID creates a new requestID.
func newRequestID(prefix string) *requestID {
	return &requestID{
		prefix: prefix,
		id:     0,
	}
}

// next generates a new request ID.
func (r *requestID) next() string {
	next := atomic.AddInt32(&r.id, 1)
	return r.prefix + strconv.Itoa(int(next))
}
