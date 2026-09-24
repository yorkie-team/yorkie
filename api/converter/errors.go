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

package converter

import (
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

// ErrorCodeOf returns the error code of the given error.
func ErrorCodeOf(err error) string {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return ""
	}
	for _, detail := range connectErr.Details() {
		msg, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}

		if errorInfo, ok := msg.(*errdetails.ErrorInfo); ok {
			return errorInfo.GetMetadata()["code"]
		}
	}
	return ""
}

// ErrorMetadataOf returns the error metadata of the given error.
func ErrorMetadataOf(err error) map[string]string {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return nil
	}
	for _, detail := range connectErr.Details() {
		msg, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}

		if errorInfo, ok := msg.(*errdetails.ErrorInfo); ok {
			return errorInfo.GetMetadata()
		}
	}
	return nil
}
