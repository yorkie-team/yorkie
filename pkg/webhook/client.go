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

// Package webhook provides an webhook utilities.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HMACTransport is a http.RoundTripper that adds an X-Signature
// header to each request using an HMAC-SHA256 signature.
type HMACTransport struct {
	PrivateKey string
}

// GenerateHMACSignature computes an HMAC-SHA256 signature for the given data
// using the specified secret key.
func GenerateHMACSignature(secret string, data []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// RoundTrip implements the http.RoundTripper interface. It reads the request body
// to compute the HMAC signature, sets the "X-Signature" header, and restores
// the body for use by subsequent transports or handlers.
func (t *HMACTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	reqCopy := r.Clone(r.Context())

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request: %w", err)
	}
	reqCopy.Body = io.NopCloser(bytes.NewBuffer(rawBody))

	signature := GenerateHMACSignature(t.PrivateKey, rawBody)
	reqCopy.Header.Set("X-Signature", signature)

	resp, err := http.DefaultTransport.RoundTrip(reqCopy)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// NewClient creates an *http.Client configured with a custom HMAC transport
// and a specified timeout. The transport will add an "X-Signature" header to
// every request using the provided private key.
func NewClient(timeout time.Duration, privateKey string) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &HMACTransport{
			PrivateKey: privateKey,
		},
	}
}
