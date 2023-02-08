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

package xml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/xml"
)

func TestFragment(t *testing.T) {
	t.Run("basic xml test", func(t *testing.T) {
		fragment := xml.NewDocumentFragment()
		assert.Equal(t, "<document></document>", fragment.String())

		fragment.Append(xml.NewText("abcd"))
		assert.Len(t, fragment.ChildNodes(), 1)
		assert.Equal(t, "<document>abcd</document>", fragment.String())

		noteElem := xml.NewElement("note")
		fragment.Append(noteElem)
		assert.Len(t, fragment.ChildNodes(), 2)
		assert.Equal(t, "<document>abcd<note></note></document>", fragment.String())

		noteElem.Append(xml.NewText("1234"))
		assert.Equal(t, "<document>abcd<note>1234</note></document>", fragment.String())

		fragment.Delete(2, 6)
		// TODO(hackerwins): uncomment this after implementing Delete.
		// assert.Equal(t, "<document>ab<note>34</note></document>", fragment.String())
	})
}
