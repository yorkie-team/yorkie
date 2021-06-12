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

import (
	"encoding/json"
	goerrors "errors"
	"io"

	"github.com/pkg/errors"
)

// VerbType represents an action taken on the document.
type VerbType string

const (

	// Read represents the case of only reading the given document.
	Read VerbType = "r"

	// ReadWrite represents the case of reading and writing the given document.
	ReadWrite VerbType = "rw"
)

var (
	// ErrInvalidWebhookRequest is returned when the given webhook request is not valid.
	ErrInvalidWebhookRequest = goerrors.New("invalid authorization webhook request")

	// ErrInvalidWebhookResponse is returned when the given webhook response is not valid.
	ErrInvalidWebhookResponse = goerrors.New("invalid authorization webhook response")
)

// Method represents a method name of RPC.
type Method string

// Belows are the names of RPCs.
const (
	ActivateClient   Method = "ActivateClient"
	DeactivateClient Method = "DeactivateClient"
	AttachDocument   Method = "AttachDocument"
	DetachDocument   Method = "DetachDocument"
	PushPull         Method = "PushPull"
)

// AccessAttribute represents an access attribute.
type AccessAttribute struct {
	Key  string   `json:"key"`
	Verb VerbType `json:"verb"`
}

// AccessInfo represents an access information.
type AccessInfo struct {
	Method     Method
	Attributes []AccessAttribute
}

// AuthWebhookRequest represents the request of authentication webhook.
type AuthWebhookRequest struct {
	Token      string            `json:"token"`
	Method     Method            `json:"method"`
	Attributes []AccessAttribute `json:"attributes"`
}

// NewAuthWebhookRequest creates a new instance of AuthWebhookRequest.
func NewAuthWebhookRequest(reader io.Reader) (*AuthWebhookRequest, error) {
	req := &AuthWebhookRequest{}

	if err := json.NewDecoder(reader).Decode(req); err != nil {
		return nil, errors.Wrapf(ErrInvalidWebhookRequest, "error: %s", err.Error())
	}

	return req, nil
}

// AuthWebhookResponse represents the response of authentication webhook.
type AuthWebhookResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// NewAuthWebhookResponse creates a new instance of AuthWebhookResponse.
func NewAuthWebhookResponse(reader io.Reader) (*AuthWebhookResponse, error) {
	resp := &AuthWebhookResponse{}

	if err := json.NewDecoder(reader).Decode(resp); err != nil {
		return nil, errors.Wrapf(ErrInvalidWebhookResponse, "error: %s", err.Error())
	}

	return resp, nil
}

// Write writes this response to the given writer.
func (r *AuthWebhookResponse) Write(writer io.Writer) (int, error) {
	resBody, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}

	return writer.Write(resBody)
}
