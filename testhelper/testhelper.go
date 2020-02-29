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

	"github.com/yorkie-team/yorkie/yorkie"
	"github.com/yorkie-team/yorkie/yorkie/backend/mongo"
)

var (
	testConfig = &yorkie.Config{
		RPCPort: 1101,
		Mongo: &mongo.Config{
			ConnectionURI:        "mongodb://localhost:27017",
			ConnectionTimeoutSec: 5,
			PingTimeoutSec:       5,
			YorkieDatabase:       "yorkie-meta",
		},
	}
)

func randBetween(min, max int) int {
	return rand.Intn(max-min) + min
}

func WithYorkie(t *testing.T, f func(*testing.T, *yorkie.Yorkie)) {
	testConfig.Mongo.YorkieDatabase = fmt.Sprintf("yorkie-meta-%d", randBetween(0, 9999))
	y, err := yorkie.New(testConfig)
	if err != nil {
		t.Fatal(err)
	}

	if err := y.Start(); err != nil {
		t.Fatal(err)
	}

	f(t, y)

	if err := y.Shutdown(true); err != nil {
		t.Error(err)
	}
}
