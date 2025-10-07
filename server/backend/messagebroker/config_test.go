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

package messagebroker_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/messagebroker"
)

func TestConfig(t *testing.T) {
	t.Run("validate test", func(t *testing.T) {
		validConf := messagebroker.Config{
			Addresses:    "localhost:8080",
			Topic:        "yorkie",
			WriteTimeout: "1s",
		}
		assert.NoError(t, validConf.Validate())

		conf1 := validConf
		conf1.Addresses = ""
		assert.Error(t, conf1.Validate())

		conf2 := validConf
		conf2.Addresses = "localhost:8080,"
		assert.Error(t, conf2.Validate())
		assert.Contains(t, conf2.Validate().Error(), conf2.Addresses)

		conf3 := validConf
		conf3.Topic = ""
		assert.Error(t, conf3.Validate())

		conf4 := validConf
		conf4.WriteTimeout = "invalid"
		assert.Error(t, conf4.Validate())
	})

	t.Run("test split addresses", func(t *testing.T) {
		c := &messagebroker.Config{
			Addresses: "localhost:8080,localhost:8081",
		}
		addrs := c.SplitAddresses()
		assert.Equal(t, []string{"localhost:8080", "localhost:8081"}, addrs)
	})

	t.Run("test must parse write timeout", func(t *testing.T) {
		c := &messagebroker.Config{
			WriteTimeout: "1s",
		}
		assert.Equal(t, time.Second, c.MustParseWriteTimeout())
	})

	t.Run("test must parse write timeout with invalid duration", func(t *testing.T) {
		c := &messagebroker.Config{
			WriteTimeout: "1",
		}
		assert.PanicsWithError(t, messagebroker.ErrInvalidDuration.Error(), func() {
			c.MustParseWriteTimeout()
		})
	})
}
