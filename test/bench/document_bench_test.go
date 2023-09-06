//go:build bench

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func BenchmarkDocument(b *testing.B) {
	b.Run("constructor test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			assert.Equal(b, doc.Checkpoint(), change.InitialCheckpoint)
			assert.False(b, doc.HasLocalChanges())
			assert.False(b, doc.IsAttached())
		}
	})

	b.Run("status test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			assert.False(b, doc.IsAttached())
			doc.SetStatus(document.StatusAttached)
			assert.True(b, doc.IsAttached())
		}
	})

	b.Run("equals test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc1 := document.New("d1")
			doc2 := document.New("d2")
			doc3 := document.New("d3")

			err := doc1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetString("k1", "v1")
				return nil
			}, "updates k1")
			assert.NoError(b, err)

			assert.NotEqual(b, doc1.Marshal(), doc2.Marshal())
			assert.Equal(b, doc2.Marshal(), doc3.Marshal())
		}
	})

	b.Run("nested update test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`

			doc := document.New("d1")
			assert.Equal(b, "{}", doc.Marshal())
			assert.False(b, doc.HasLocalChanges())

			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetString("k1", "v1")
				root.SetNewObject("k2").SetString("k4", "v4")
				root.SetNewArray("k3").AddString("v5", "v6")
				assert.Equal(b, expected, root.Marshal())
				return nil
			}, "updates k1,k2,k3")
			assert.NoError(b, err)

			assert.Equal(b, expected, doc.Marshal())
			assert.True(b, doc.HasLocalChanges())
		}
	})

	b.Run("delete test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			assert.Equal(b, "{}", doc.Marshal())
			assert.False(b, doc.HasLocalChanges())

			expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`
			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetString("k1", "v1")
				root.SetNewObject("k2").SetString("k4", "v4")
				root.SetNewArray("k3").AddString("v5", "v6")
				assert.Equal(b, expected, root.Marshal())
				return nil
			}, "updates k1,k2,k3")
			assert.NoError(b, err)
			assert.Equal(b, expected, doc.Marshal())

			expected = `{"k1":"v1","k3":["v5","v6"]}`
			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.Delete("k2")
				assert.Equal(b, expected, root.Marshal())
				return nil
			}, "deletes k2")
			assert.NoError(b, err)
			assert.Equal(b, expected, doc.Marshal())
		}
	})

	b.Run("object test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetString("k1", "v1")
				assert.Equal(b, `{"k1":"v1"}`, root.Marshal())
				root.SetString("k1", "v2")
				assert.Equal(b, `{"k1":"v2"}`, root.Marshal())
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"k1":"v2"}`, doc.Marshal())
		}
	})

	b.Run("array test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewArray("k1").AddInteger(1).AddInteger(2).AddInteger(3)
				assert.Equal(b, 3, root.GetArray("k1").Len())
				assert.Equal(b, `{"k1":[1,2,3]}`, root.Marshal())
				assert.Equal(b, "[0,0]0[1,1]1[2,1]2[3,1]3", root.GetArray("k1").StructureAsString())

				root.GetArray("k1").Delete(1)
				assert.Equal(b, `{"k1":[1,3]}`, root.Marshal())
				assert.Equal(b, 2, root.GetArray("k1").Len())
				assert.Equal(b, "[0,0]0[1,1]1[2,0]2[1,1]3", root.GetArray("k1").StructureAsString())

				root.GetArray("k1").InsertIntegerAfter(0, 2)
				assert.Equal(b, `{"k1":[1,2,3]}`, root.Marshal())
				assert.Equal(b, 3, root.GetArray("k1").Len())
				assert.Equal(b, "[0,0]0[1,1]1[3,1]2[1,0]2[1,1]3", root.GetArray("k1").StructureAsString())

				root.GetArray("k1").InsertIntegerAfter(2, 4)
				assert.Equal(b, `{"k1":[1,2,3,4]}`, root.Marshal())
				assert.Equal(b, 4, root.GetArray("k1").Len())
				assert.Equal(b, "[0,0]0[1,1]1[2,1]2[2,0]2[3,1]3[4,1]4", root.GetArray("k1").StructureAsString())

				for i := 0; i < root.GetArray("k1").Len(); i++ {
					assert.Equal(
						b,
						fmt.Sprintf("%d", i+1),
						root.GetArray("k1").Get(i).Marshal(),
					)
				}

				return nil
			})

			assert.NoError(b, err)
		}
	})

	b.Run("text test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			//           ---------- ins links --------
			//           |                |          |
			// [init] - [A] - [12] - [BC deleted] - [D]
			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("k1").
					Edit(0, 0, "ABCD").
					Edit(1, 3, "12")
				assert.Equal(b, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, root.Marshal())
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, doc.Marshal())

			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				assert.Equal(b,
					`[0:0:00:0 {} ""][1:2:00:0 {} "A"][1:3:00:0 {} "12"]{1:2:00:1 {} "BC"}[1:2:00:3 {} "D"]`,
					text.StructureAsString(),
				)

				from, _ := text.CreateRange(0, 0)
				assert.Equal(b, "0:0:00:0:0", from.StructureAsString())

				from, _ = text.CreateRange(1, 1)
				assert.Equal(b, "1:2:00:0:1", from.StructureAsString())

				from, _ = text.CreateRange(2, 2)
				assert.Equal(b, "1:3:00:0:1", from.StructureAsString())

				from, _ = text.CreateRange(3, 3)
				assert.Equal(b, "1:3:00:0:2", from.StructureAsString())

				from, _ = text.CreateRange(4, 4)
				assert.Equal(b, "1:2:00:3:1", from.StructureAsString())
				return nil
			})
			assert.NoError(b, err)
		}
	})

	b.Run("text composition test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("k1").
					Edit(0, 0, "ㅎ").
					Edit(0, 1, "하").
					Edit(0, 1, "한").
					Edit(0, 1, "하").
					Edit(1, 1, "느").
					Edit(1, 2, "늘")
				assert.Equal(b, `{"k1":[{"val":"하"},{"val":"늘"}]}`, root.Marshal())
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"k1":[{"val":"하"},{"val":"늘"}]}`, doc.Marshal())
		}
	})

	b.Run("rich text test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				text := root.SetNewText("k1")
				text.Edit(0, 0, "Hello world", nil)
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {} "Hello world"]`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"k1":[{"val":"Hello world"}]}`, doc.Marshal())

			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Style(0, 5, map[string]string{"b": "1"})
				assert.Equal(b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"]`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hello"},{"val":" world"}]}`,
				doc.Marshal(),
			)

			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Style(0, 5, map[string]string{"b": "1"})
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"]`,
					text.StructureAsString(),
				)

				text.Style(3, 5, map[string]string{"i": "1"})
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][1:2:00:5 {} " world"]`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" world"}]}`,
				doc.Marshal(),
			)

			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Edit(5, 11, " Yorkie", nil)
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"]`+
						`[4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" Yorkie"}]}`,
				doc.Marshal(),
			)

			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				text := root.GetText("k1")
				text.Edit(5, 5, "\n", map[string]string{"list": "true"})
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][5:1:00:0 {"list":"true"} "\n"][4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"attrs":{"list":"true"},"val":"\n"},{"val":" Yorkie"}]}`,
				doc.Marshal(),
			)
		}
	})

	b.Run("counter test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			var integer = 10
			var long int64 = 5
			var uinteger uint = 100
			var float float32 = 3.14
			var double = 5.66

			// integer type test
			err := doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewCounter("age", crdt.IntegerCnt, 5)

				age := root.GetCounter("age")
				age.Increase(long)
				age.Increase(double)
				age.Increase(float)
				age.Increase(uinteger)
				age.Increase(integer)

				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"age":128}`, doc.Marshal())

			// long type test
			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewCounter("price", crdt.LongCnt, 9000000000000000000)
				price := root.GetCounter("price")
				price.Increase(long)
				price.Increase(double)
				price.Increase(float)
				price.Increase(uinteger)
				price.Increase(integer)

				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"age":128,"price":9000000000000000123}`, doc.Marshal())

			// negative operator test
			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				age := root.GetCounter("age")
				age.Increase(-5)
				age.Increase(-3.14)

				price := root.GetCounter("price")
				price.Increase(-100)
				price.Increase(-20.5)

				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"age":120,"price":9000000000000000003}`, doc.Marshal())

			// TODO: it should be modified to error check
			// when 'Remove panic from server code (#50)' is completed.
			err = doc.Update(func(root *json.Object, p *presence.Presence) error {
				defer func() {
					r := recover()
					assert.NotNil(b, r)
					assert.Equal(b, r, "unsupported type")
				}()

				var notAllowType uint64 = 18300000000000000000
				age := root.GetCounter("age")
				age.Increase(notAllowType)

				return nil
			})
			assert.NoError(b, err)
			assert.Equal(b, `{"age":120,"price":9000000000000000003}`, doc.Marshal())
		}
	})

	b.Run("text edit gc 100", func(b *testing.B) {
		benchmarkTextEditGC(100, b)
	})

	b.Run("text edit gc 1000", func(b *testing.B) {
		benchmarkTextEditGC(1000, b)
	})

	b.Run("text split gc 100", func(b *testing.B) {
		benchmarkTextSplitGC(100, b)
	})

	b.Run("text split gc 1000", func(b *testing.B) {
		benchmarkTextSplitGC(1000, b)
	})

	b.Run("text delete all 10000", func(b *testing.B) {
		benchmarkTextDeleteAll(10000, b)
	})

	b.Run("text delete all 100000", func(b *testing.B) {
		benchmarkTextDeleteAll(100000, b)
	})

	b.Run("text 100", func(b *testing.B) {
		benchmarkText(100, b)
	})

	b.Run("text 1000", func(b *testing.B) {
		benchmarkText(1000, b)
	})

	b.Run("array 1000", func(b *testing.B) {
		benchmarkArray(1000, b)
	})

	b.Run("array 10000", func(b *testing.B) {
		benchmarkArray(10000, b)
	})

	b.Run("array gc 100", func(b *testing.B) {
		benchmarkArrayGC(100, b)
	})

	b.Run("array gc 1000", func(b *testing.B) {
		benchmarkArrayGC(1000, b)
	})

	b.Run("counter 1000", func(b *testing.B) {
		benchmarkCounter(1000, b)
	})

	b.Run("counter 10000", func(b *testing.B) {
		benchmarkCounter(10000, b)
	})

	b.Run("object 1000", func(b *testing.B) {
		benchmarkObject(1000, b)
	})

	b.Run("object 10000", func(b *testing.B) {
		benchmarkObject(10000, b)
	})

	b.Run("tree 100", func(b *testing.B) {
		benchmarkTree(100, b)
	})

	b.Run("tree 1000", func(b *testing.B) {
		benchmarkTree(1000, b)
	})

	b.Run("tree 10000", func(b *testing.B) {
		benchmarkTree(10000, b)
	})

	b.Run("tree delete all 1000", func(b *testing.B) {
		benchmarkTreeDeleteAll(1000, b)
	})

	b.Run("tree edit gc 100", func(b *testing.B) {
		benchmarkTreeEditGC(100, b)
	})

	b.Run("tree edit gc 1000", func(b *testing.B) {
		benchmarkTreeEditGC(1000, b)
	})

	b.Run("tree split gc 100", func(b *testing.B) {
		benchmarkTreeSplitGC(100, b)
	})

	b.Run("tree split gc 1000", func(b *testing.B) {
		benchmarkTreeSplitGC(1000, b)
	})

}

func benchmarkTree(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			for c := 1; c <= cnt; c++ {
				tree.Edit(c, c, &json.TreeNode{Type: "text", Value: "a"})
			}
			return nil
		})
		assert.NoError(b, err)
	}
}

func benchmarkTreeDeleteAll(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			for c := 1; c <= cnt; c++ {
				tree.Edit(c, c, &json.TreeNode{Type: "text", Value: "a"})
			}
			return nil
		})

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			tree.Edit(1, cnt+1)

			return nil
		})
		assert.NoError(b, err)
	}
}

func benchmarkTreeEditGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			for c := 1; c <= cnt; c++ {
				tree.Edit(c, c, &json.TreeNode{Type: "text", Value: "a"})
			}
			return nil
		})

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			for c := 1; c <= cnt; c++ {
				tree.Edit(c, c+1, &json.TreeNode{Type: "text", Value: "b"})
			}

			return nil
		})
		assert.NoError(b, err)
		assert.Equal(b, cnt, doc.GarbageLen())
		assert.Equal(b, cnt, doc.GarbageCollect(time.MaxTicket))
	}
}

func benchmarkTreeSplitGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		var builder strings.Builder
		for i := 0; i < cnt; i++ {
			builder.WriteString("a")
		}
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			tree.Edit(1, 1, &json.TreeNode{Type: "text", Value: builder.String()})

			return nil
		})

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			tree := root.GetTree("t")
			for c := 1; c <= cnt; c++ {
				tree.Edit(c, c+1, &json.TreeNode{Type: "text", Value: "b"})
			}

			return nil
		})
		assert.NoError(b, err)
		assert.Equal(b, cnt, doc.GarbageLen())
		assert.Equal(b, cnt, doc.GarbageCollect(time.MaxTicket))
	}
}

func benchmarkText(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.SetNewText("k1")
			for c := 0; c < cnt; c++ {
				text.Edit(c, c, "a")
			}
			return nil
		})
		assert.NoError(b, err)
	}
}

func benchmarkTextDeleteAll(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.SetNewText("k1")
			for c := 0; c < cnt; c++ {
				text.Edit(c, c, "a")
			}
			return nil
		}, "Create cnt-length text to test")
		assert.NoError(b, err)

		b.StartTimer()
		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			text.Edit(0, cnt, "")
			return nil
		}, "Delete all at a time")
		assert.NoError(b, err)

		assert.Equal(b, `{"k1":[]}`, doc.Marshal())
	}
}

func benchmarkTextEditGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")
		assert.Equal(b, "{}", doc.Marshal())
		assert.False(b, doc.HasLocalChanges())

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.SetNewText("k1")
			for i := 0; i < cnt; i++ {
				text.Edit(i, i, "a")
			}
			return nil
		}, "creates a text then appends a")
		assert.NoError(b, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k1")
			for i := 0; i < cnt; i++ {
				text.Edit(i, i+1, "b")
			}
			return nil
		}, "replace contents with b")
		assert.NoError(b, err)
		assert.Equal(b, cnt, doc.GarbageLen())
		assert.Equal(b, cnt, doc.GarbageCollect(time.MaxTicket))
	}
}

func benchmarkTextSplitGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")
		assert.Equal(b, "{}", doc.Marshal())
		assert.False(b, doc.HasLocalChanges())
		var builder strings.Builder
		for i := 0; i < cnt; i++ {
			builder.WriteString("a")
		}
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.SetNewText("k2")
			text.Edit(0, 0, builder.String())
			return nil
		}, "initial")
		assert.NoError(b, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText("k2")
			for i := 0; i < cnt; i++ {
				if i != cnt {
					text.Edit(i, i+1, "b")
				}
			}
			return nil
		}, "Modify one node multiple times")
		assert.NoError(b, err)

		assert.Equal(b, cnt, doc.GarbageLen())
		assert.Equal(b, cnt, doc.GarbageCollect(time.MaxTicket))
	}
}

func benchmarkArray(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			array := root.SetNewArray("k1")
			for c := 0; c < cnt; c++ {
				array.AddInteger(c)
			}
			return nil
		})
		assert.NoError(b, err)
	}
}

func benchmarkArrayGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("1")
			for i := 0; i < cnt; i++ {
				root.GetArray("1").AddInteger(i)
			}

			return nil
		}, "creates an array then adds integers")
		assert.NoError(b, err)

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("1")
			return nil
		}, "deletes the array")
		assert.NoError(b, err)

		assert.Equal(b, cnt+1, doc.GarbageCollect(time.MaxTicket))
		assert.NoError(b, err)
	}
}

func benchmarkCounter(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			counter := root.SetNewCounter("k1", crdt.IntegerCnt, 0)
			for c := 0; c < cnt; c++ {
				counter.Increase(c)
			}
			return nil
		})
		assert.NoError(b, err)
	}
}

func benchmarkObject(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			for c := 0; c < cnt; c++ {
				root.SetInteger("k1", c)
			}
			return nil
		})
		assert.NoError(b, err)
	}
}
