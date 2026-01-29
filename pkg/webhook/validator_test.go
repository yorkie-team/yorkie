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

package webhook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/webhook"
)

func TestValidateWebhookURL_SSRF(t *testing.T) {
	tcs := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// scheme
		{"block non-http scheme file", "file:///etc/passwd", true},
		{"block non-http scheme gopher", "gopher://127.0.0.1:70", true},

		// loopback / localhost
		{"block localhost", "http://localhost/webhook", true},
		{"block 127.0.0.1", "http://127.0.0.1/webhook", true},
		{"block ipv6 loopback", "http://[::1]/webhook", true},

		// unspecified ip
		{"block unspecified ip", "http://0.0.0.0/webhook", true},
		{"block ipv6 unspecified ::", "http://[::]/webhook", true},

		// private ranges
		{"block 10.0.0.0/8", "http://10.0.0.1/webhook", true},
		{"block 172.16.0.0/12", "http://172.16.0.1/webhook", true},
		{"block 192.168.0.0/16", "http://192.168.0.1/webhook", true},

		// link-local (incl. metadata families)
		{"block link-local 169.254/16", "http://169.254.1.1/webhook", true},
		{"block cloud metadata 169.254.169.254", "http://169.254.169.254/latest/meta-data", true},

		// multicast
		{"block multicast", "http://224.0.0.1/webhook", true},

		// allow examples (public IP literal)
		{"allow public ip 1.1.1.1", "https://1.1.1.1/webhook", false},
		{"allow public ip 8.8.8.8", "https://8.8.8.8/webhook", false},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			err := webhook.ValidateWebhookURL(tc.url, false)
			if tc.wantErr {
				assert.Error(t, err)
				assert.ErrorContains(t, err, webhook.ErrInvalidWebhookURL.Error())
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateWebhookURL_Disabled(t *testing.T) {
	t.Run("allow blocked url if validation is disabled", func(t *testing.T) {
		err := webhook.ValidateWebhookURL("http://localhost/webhook", true)
		assert.NoError(t, err)
	})
}
