/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package webhook

import (
	"fmt"
	"net"
	"net/url"

	"github.com/yorkie-team/yorkie/pkg/errors"
)

var (
	// ErrInvalidWebhookURL is returned when the webhook URL is invalid or blocked for security reasons.
	ErrInvalidWebhookURL = errors.InvalidArgument("invalid webhook URL").WithCode("ErrInvalidWebhookURL")
)

// ValidateWebhookURL validates the webhook URL to prevent SSRF attacks.
func ValidateWebhookURL(rawURL string, disabled bool) error {
	// If validation is disabled, skip the validation.
	if disabled {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: parse url: %v", ErrInvalidWebhookURL, err)
	}

	// Allow only http / https
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme", ErrInvalidWebhookURL)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: empty hostname", ErrInvalidWebhookURL)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		// DNS lookup failure → block (fail closed)
		return fmt.Errorf("%w: dns lookup failed", ErrInvalidWebhookURL)
	}

	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("%w: blocked ip address %s", ErrInvalidWebhookURL, ip)
		}
	}

	return nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Unspecified IP (0.0.0.0, ::)
	if ip.IsUnspecified() {
		return true
	}

	// Loopback (127.0.0.1, ::1)
	if ip.IsLoopback() {
		return true
	}

	// Private ranges (RFC1918, ULA)
	if ip.IsPrivate() {
		return true
	}

	// Link-local (169.254.0.0/16, fe80::/10) incl. metadata services
	if ip.IsLinkLocalUnicast() {
		return true
	}

	// Multicast
	if ip.IsMulticast() {
		return true
	}

	return false
}
