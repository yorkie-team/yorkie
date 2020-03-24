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
	"log"
	"time"

	"github.com/yorkie-team/yorkie/yorkie"
)

var testStartedAt int64

const (
	TestPort               = 1101
	TestMongoConnectionURI = "mongodb://localhost:27017"
)

func init() {
	now := time.Now()
	testStartedAt = now.Unix()
}

// TestDBName returns the name of test database with timestamp.
// timestamp is set only once on first call.
func TestDBName() string {
	return fmt.Sprintf("test-%s-%d", yorkie.DefaultYorkieDatabase, testStartedAt)
}

// TestYorkie is return Yorkie instance for testing.
func TestYorkie() *yorkie.Yorkie {
	conf := yorkie.NewConfigWithPortAndDBName(TestPort, TestDBName())
	y, err := yorkie.New(conf)
	if err != nil {
		log.Fatal(err)
	}
	return y
}
