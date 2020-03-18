/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package testhelper

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/yorkie"
)

const (
	testDBNameFmt = "test-%s-%d"
	testPort      = 1101
)

var testStartedAt int64

func init() {
	now := time.Now()
	testStartedAt = now.Unix()
}

func randBetween(min, max int) int {
	return rand.Intn(max-min) + min
}

func WithYorkie(t *testing.T, f func(*testing.T, *yorkie.Yorkie)) {
	testDBName := fmt.Sprintf(testDBNameFmt, yorkie.DefaultYorkieDatabase, testStartedAt)
	conf := yorkie.NewConfigForTest(testPort, testDBName)
	y, err := yorkie.New(conf)
	if err != nil {
		t.Fatal(err)
	}

	if err := y.Start(); err != nil {
		t.Fatal(err)
	}

	f(t, y)

	err = y.Shutdown(true)
	assert.Nil(t, err)
}
