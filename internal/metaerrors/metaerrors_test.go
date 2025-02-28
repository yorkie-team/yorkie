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

package metaerrors_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/internal/metaerrors"
)

func TestMetaError(t *testing.T) {
	t.Run("test meta error", func(t *testing.T) {
		err := errors.New("error message")
		metaErr := metaerrors.New(err, map[string]string{"key": "value"})
		assert.Equal(t, "error message [key=value]", metaErr.Error())
	})

	t.Run("test meta error without metadata", func(t *testing.T) {
		err := errors.New("error message")
		metaErr := metaerrors.New(err, nil)
		assert.Equal(t, "error message", metaErr.Error())
	})

	t.Run("test meta error with wrapped error", func(t *testing.T) {
		err := fmt.Errorf("wrapped error: %w", errors.New("error message"))
		metaErr := metaerrors.New(err, map[string]string{"key": "value"})
		assert.Equal(t, "wrapped error: error message [key=value]", metaErr.Error())

		metaErr = metaerrors.New(errors.New("error message"), map[string]string{"key": "value"})
		assert.Equal(t, "error message [key=value]", metaErr.Error())

		wrappedErr := fmt.Errorf("wrapped error: %w", metaErr)
		assert.Equal(t, "wrapped error: error message [key=value]", wrappedErr.Error())
	})
}
