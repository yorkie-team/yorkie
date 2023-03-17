/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

package key

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKey(t *testing.T) {
	t.Run("valid key test", func(t *testing.T) {
		err := Key("valid-key").Validate()
		assert.Nil(t, err, "key should be valid")

		err = Key("valid-key-1").Validate()
		assert.Nil(t, err, "key should be valid")

		err = Key("fdsxfdsf").Validate()
		assert.Nil(t, err, "key should be valid")

		err = Key("-----_________________-a").Validate()
		assert.Nil(t, err, "key should be valid")

		err = Key("Capital-Character-Key").Validate()
		assert.Nil(t, err, "key should be valid")
	})

	t.Run("invalid key test", func(t *testing.T) {
		err := Key("invalid key").Validate() // space is not allowed
		assert.Equal(t, err, ErrInvalidKey, "space is not allowed")

		err = Key("invalid-key-~$a").Validate() // special character $ is not allowed
		assert.Equal(t, err, ErrInvalidKey, "special character $ is not allowed")

		err = Key("invalid-key-$").Validate() // special character $ is not allowed
		assert.Equal(t, err, ErrInvalidKey, "special character $ is not allowed")

		err = Key(strings.Repeat("invalid-key-sample", 8)).Validate()
		assert.Equal(t, err, ErrInvalidKey, "over 120 characters is not allowed")

		err = Key("inv").Validate()
		assert.Equal(t, err, ErrInvalidKey, "less than 4 characters is not allowed")
	})
}
