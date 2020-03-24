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

package client_test

import (
	"log"
	"os"
	"testing"

	"github.com/yorkie-team/yorkie/testhelper"
	"github.com/yorkie-team/yorkie/yorkie"
)

var testYorkie *yorkie.Yorkie

func TestMain(m *testing.M) {
	setup()
	code := m.Run()
	teardown()
	os.Exit(code)
}

func setup() {
	y := testhelper.TestYorkie()
	if err := y.Start(); err != nil {
		log.Fatal(err)
	}
	testYorkie = y
}

func teardown() {
	if testYorkie != nil {
		if err := testYorkie.Shutdown(true); err != nil {
			log.Println(err)
		}
	}
}
