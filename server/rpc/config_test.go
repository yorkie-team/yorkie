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

package rpc_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/rpc"
)

func TestConfigValidate(t *testing.T) {
	validConf := rpc.Config{
		Port:              11101,
		ReadHeaderTimeout: "5s",
		IdleTimeout:       "2m",
	}
	assert.NoError(t, validConf.Validate())

	for _, invalid := range []string{"", "0s", "-5s", "5x"} {
		conf := validConf
		conf.ReadHeaderTimeout = invalid
		assert.Error(t, conf.Validate(), "ReadHeaderTimeout=%q", invalid)

		conf = validConf
		conf.IdleTimeout = invalid
		assert.Error(t, conf.Validate(), "IdleTimeout=%q", invalid)
	}

	readHeaderTimeout, err := validConf.ParseReadHeaderTimeout()
	assert.NoError(t, err)
	assert.Equal(t, 5*time.Second, readHeaderTimeout)

	idleTimeout, err := validConf.ParseIdleTimeout()
	assert.NoError(t, err)
	assert.Equal(t, 2*time.Minute, idleTimeout)
}
