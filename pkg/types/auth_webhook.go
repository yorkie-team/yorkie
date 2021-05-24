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

package types

// VerbType represents an action taken on the document.
type VerbType string

const (
	// Read represents the case of only reading the given document.
	Read VerbType = "r"

	// ReadWrite represents the case of reading and writing the given document.
	ReadWrite VerbType = "rw"
)

// AccessAttribute represents an access attribute.
type AccessAttribute struct {
	Key  string   `json:"key"`
	Verb VerbType `json:"verb"`
}

// AccessInfo represents an access information.
type AccessInfo struct {
	Method     string
	Attributes []AccessAttribute
}

// AuthWebhookRequest represents the request of authentication webhook.
type AuthWebhookRequest struct {
	Token      string            `json:"token"`
	Method     string            `json:"method"`
	Attributes []AccessAttribute `json:"attributes"`
}

// AuthWebhookResponse represents the response of authentication webhook.
type AuthWebhookResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}
