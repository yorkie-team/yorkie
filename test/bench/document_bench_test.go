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

			err := doc1.Update(func(root *json.Object) error {
				root.SetString("k1", "v1")
				return nil
			}, "updates k1")
			assert.NoError(b, err)

			dm1, _ := doc1.Marshal()
			dm2, _ := doc2.Marshal()
			dm3, _ := doc3.Marshal()
			assert.NotEqual(b, dm1, dm2)
			assert.Equal(b, dm2, dm3)
		}
	})

	b.Run("nested update test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`

			doc := document.New("d1")
			dm, _ := doc.Marshal()
			assert.Equal(b, "{}", dm)
			assert.False(b, doc.HasLocalChanges())

			err := doc.Update(func(root *json.Object) error {
				_, _ = root.SetString("k1", "v1")
				obj, _ := root.SetNewObject("k2")
				obj.SetString("k4", "v4")
				arr, _ := root.SetNewArray("k3")
				arr.AddString("v5", "v6")
				rm, _ := root.Marshal()
				assert.Equal(b, expected, rm)
				return nil
			}, "updates k1,k2,k3")
			assert.NoError(b, err)

			dm, _ = doc.Marshal()
			assert.Equal(b, expected, dm)
			assert.True(b, doc.HasLocalChanges())
		}
	})

	b.Run("delete test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			dm, _ := doc.Marshal()
			assert.Equal(b, "{}", dm)
			assert.False(b, doc.HasLocalChanges())

			expected := `{"k1":"v1","k2":{"k4":"v4"},"k3":["v5","v6"]}`
			err := doc.Update(func(root *json.Object) error {
				_, _ = root.SetString("k1", "v1")
				obj, _ := root.SetNewObject("k2")
				obj.SetString("k4", "v4")
				arr, _ := root.SetNewArray("k3")
				arr.AddString("v5", "v6")
				rm, _ := root.Marshal()
				assert.Equal(b, expected, rm)
				return nil
			}, "updates k1,k2,k3")
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(b, expected, dm)

			expected = `{"k1":"v1","k3":["v5","v6"]}`
			err = doc.Update(func(root *json.Object) error {
				root.Delete("k2")
				rm, _ := root.Marshal()
				assert.Equal(b, expected, rm)
				return nil
			}, "deletes k2")
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(b, expected, dm)
		}
	})

	b.Run("object test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")
			err := doc.Update(func(root *json.Object) error {
				root.SetString("k1", "v1")
				rm, _ := root.Marshal()
				assert.Equal(b, `{"k1":"v1"}`, rm)
				root.SetString("k1", "v2")
				rm, _ = root.Marshal()
				assert.Equal(b, `{"k1":"v2"}`, rm)
				return nil
			})
			assert.NoError(b, err)
			dm, _ := doc.Marshal()
			assert.Equal(b, `{"k1":"v2"}`, dm)
		}
	})

	b.Run("array test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			err := doc.Update(func(root *json.Object) error {
				arr, _ := root.SetNewArray("k1")
				arr.AddInteger(1)
				arr.AddInteger(2)
				arr.AddInteger(3)
				arr, _ = root.GetArray("k1")
				assert.Equal(b, 3, arr.Len())
				rm, _ := root.Marshal()
				assert.Equal(b, `{"k1":[1,2,3]}`, rm)
				arr, _ = root.GetArray("k1")
				str, _ := arr.StructureAsString()
				assert.Equal(b, "[0,0]0[1,1]1[2,1]2[3,1]3", str)

				arr, _ = root.GetArray("k1")
				arr.Delete(1)
				rm, _ = root.Marshal()
				assert.Equal(b, `{"k1":[1,3]}`, rm)
				arr, _ = root.GetArray("k1")
				assert.Equal(b, 2, arr.Len())
				arr, _ = root.GetArray("k1")
				str, _ = arr.StructureAsString()
				assert.Equal(b, "[0,0]0[1,1]1[2,0]2[1,1]3", str)

				arr, _ = root.GetArray("k1")
				arr.InsertIntegerAfter(0, 2)
				rm, _ = root.Marshal()
				assert.Equal(b, `{"k1":[1,2,3]}`, rm)
				arr, _ = root.GetArray("k1")
				assert.Equal(b, 3, arr.Len())
				arr, _ = root.GetArray("k1")
				str, _ = arr.StructureAsString()
				assert.Equal(b, "[0,0]0[1,1]1[3,1]2[1,0]2[1,1]3", str)

				arr, _ = root.GetArray("k1")
				arr.InsertIntegerAfter(2, 4)
				rm, _ = root.Marshal()
				assert.Equal(b, `{"k1":[1,2,3,4]}`, rm)
				arr, _ = root.GetArray("k1")
				assert.Equal(b, 4, arr.Len())
				arr, _ = root.GetArray("k1")
				str, _ = arr.StructureAsString()
				assert.Equal(b, "[0,0]0[1,1]1[2,1]2[2,0]2[3,1]3[4,1]4", str)

				arr, _ = root.GetArray("k1")
				for i := 0; i < arr.Len(); i++ {
					rgaTreeListNodeArr, _ := root.GetArray("k1")
					rgaTreeListNode, _ := rgaTreeListNodeArr.Get(i)
					rma, _ := rgaTreeListNode.Marshal()
					assert.Equal(
						b,
						fmt.Sprintf("%d", i+1),
						rma,
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
			err := doc.Update(func(root *json.Object) error {
				txt, _ := root.SetNewText("k1")
				txt.Edit(0, 0, "ABCD")
				txt.Edit(1, 3, "12")
				rm, _ := root.Marshal()
				assert.Equal(b, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, rm)
				return nil
			})
			assert.NoError(b, err)
			dm, _ := doc.Marshal()
			assert.Equal(b, `{"k1":[{"val":"A"},{"val":"12"},{"val":"D"}]}`, dm)

			err = doc.Update(func(root *json.Object) error {
				text, _ := root.GetText("k1")
				assert.Equal(b,
					`[0:0:00:0 {} ""][1:2:00:0 {} "A"][1:3:00:0 {} "12"]{1:2:00:1 {} "BC"}[1:2:00:3 {} "D"]`,
					text.StructureAsString(),
				)

				from, _, _ := text.CreateRange(0, 0)
				assert.Equal(b, "0:0:00:0:0", from.StructureAsString())

				from, _, _ = text.CreateRange(1, 1)
				assert.Equal(b, "1:2:00:0:1", from.StructureAsString())

				from, _, _ = text.CreateRange(2, 2)
				assert.Equal(b, "1:3:00:0:1", from.StructureAsString())

				from, _, _ = text.CreateRange(3, 3)
				assert.Equal(b, "1:3:00:0:2", from.StructureAsString())

				from, _, _ = text.CreateRange(4, 4)
				assert.Equal(b, "1:2:00:3:1", from.StructureAsString())
				return nil
			})
			assert.NoError(b, err)
		}
	})

	b.Run("text composition test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			err := doc.Update(func(root *json.Object) error {
				txt, _ := root.SetNewText("k1")
				txt.Edit(0, 0, "ㅎ")
				txt.Edit(0, 1, "하")
				txt.Edit(0, 1, "한")
				txt.Edit(0, 1, "하")
				txt.Edit(1, 1, "느")
				txt.Edit(1, 2, "늘")
				rm, _ := root.Marshal()
				assert.Equal(b, `{"k1":[{"val":"하"},{"val":"늘"}]}`, rm)
				return nil
			})
			assert.NoError(b, err)
			dm, _ := doc.Marshal()
			assert.Equal(b, `{"k1":[{"val":"하"},{"val":"늘"}]}`, dm)
		}
	})

	b.Run("rich text test", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			doc := document.New("d1")

			err := doc.Update(func(root *json.Object) error {
				text, _ := root.SetNewText("k1")
				text.Edit(0, 0, "Hello world", nil)
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {} "Hello world"]`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			dm, _ := doc.Marshal()
			assert.Equal(
				b,
				`{"k1":[{"val":"Hello world"}]}`,
				dm,
			)

			err = doc.Update(func(root *json.Object) error {
				text, _ := root.GetText("k1")
				_, _ = text.Style(0, 5, map[string]string{"b": "1"})
				assert.Equal(b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"]`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(b, `{"k1":[{"attrs":{"b":"1"},"val":"Hello"},{"val":" world"}]}`, dm)

			err = doc.Update(func(root *json.Object) error {
				text, _ := root.GetText("k1")
				_, _ = text.Style(0, 5, map[string]string{"b": "1"})
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hello"][1:2:00:5 {} " world"]`,
					text.StructureAsString(),
				)

				_, _ = text.Style(3, 5, map[string]string{"i": "1"})
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][1:2:00:5 {} " world"]`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" world"}]}`,
				dm,
			)

			err = doc.Update(func(root *json.Object) error {
				text, _ := root.GetText("k1")
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
			dm, _ = doc.Marshal()
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"val":" Yorkie"}]}`,
				dm,
			)

			err = doc.Update(func(root *json.Object) error {
				text, _ := root.GetText("k1")
				text.Edit(5, 5, "\n", map[string]string{"list": "true"})
				assert.Equal(
					b,
					`[0:0:00:0 {} ""][1:2:00:0 {"b":"1"} "Hel"][1:2:00:3 {"b":"1","i":"1"} "lo"][5:1:00:0 {"list":"true"} "\n"][4:1:00:0 {} " Yorkie"]{1:2:00:5 {} " world"}`,
					text.StructureAsString(),
				)
				return nil
			})
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(
				b,
				`{"k1":[{"attrs":{"b":"1"},"val":"Hel"},{"attrs":{"b":"1","i":"1"},"val":"lo"},{"attrs":{"list":"true"},"val":"\n"},{"val":" Yorkie"}]}`,
				dm,
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
			err := doc.Update(func(root *json.Object) error {
				root.SetNewCounter("age", crdt.IntegerCnt, 5)

				age, _ := root.GetCounter("age")
				age.Increase(long)
				age.Increase(double)
				age.Increase(float)
				age.Increase(uinteger)
				age.Increase(integer)

				return nil
			})
			assert.NoError(b, err)
			dm, _ := doc.Marshal()
			assert.Equal(b, `{"age":128}`, dm)

			// long type test
			err = doc.Update(func(root *json.Object) error {
				root.SetNewCounter("price", crdt.LongCnt, 9000000000000000000)
				price, _ := root.GetCounter("price")
				price.Increase(long)
				price.Increase(double)
				price.Increase(float)
				price.Increase(uinteger)
				price.Increase(integer)

				return nil
			})
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(b, `{"age":128,"price":9000000000000000123}`, dm)

			// negative operator test
			err = doc.Update(func(root *json.Object) error {
				age, _ := root.GetCounter("age")
				age.Increase(-5)
				age.Increase(-3.14)

				price, _ := root.GetCounter("price")
				price.Increase(-100)
				price.Increase(-20.5)

				return nil
			})
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(b, `{"age":120,"price":9000000000000000003}`, dm)

			// TODO: it should be modified to error check
			// when 'Remove panic from server code (#50)' is completed.
			err = doc.Update(func(root *json.Object) error {
				var notAllowType uint64 = 18300000000000000000
				age, _ := root.GetCounter("age")
				_, err := age.Increase(notAllowType)
				assert.Error(b, err, "unsupported type")

				return nil
			})
			assert.NoError(b, err)
			dm, _ = doc.Marshal()
			assert.Equal(b, `{"age":120,"price":9000000000000000003}`, dm)
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
}

func benchmarkText(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k1")
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

		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k1")
			for c := 0; c < cnt; c++ {
				text.Edit(c, c, "a")
			}
			return nil
		}, "Create cnt-length text to test")
		assert.NoError(b, err)

		b.StartTimer()
		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			text.Edit(0, cnt, "")
			return nil
		}, "Delete all at a time")
		assert.NoError(b, err)

		dm, _ := doc.Marshal()
		assert.Equal(b, `{"k1":[]}`, dm)
	}
}

func benchmarkTextEditGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")
		dm, _ := doc.Marshal()
		assert.Equal(b, "{}", dm)
		assert.False(b, doc.HasLocalChanges())

		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k1")
			for i := 0; i < cnt; i++ {
				text.Edit(i, i, "a")
			}
			return nil
		}, "creates a text then appends a")
		assert.NoError(b, err)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k1")
			for i := 0; i < cnt; i++ {
				text.Edit(i, i+1, "b")
			}
			return nil
		}, "replace contents with b")
		assert.NoError(b, err)
		assert.Equal(b, cnt, doc.GarbageLen())
		gc, _ := doc.GarbageCollect(time.MaxTicket)
		assert.Equal(b, cnt, gc)
	}
}

func benchmarkTextSplitGC(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")
		dm, _ := doc.Marshal()
		assert.Equal(b, "{}", dm)
		assert.False(b, doc.HasLocalChanges())
		var builder strings.Builder
		for i := 0; i < cnt; i++ {
			builder.WriteString("a")
		}
		err := doc.Update(func(root *json.Object) error {
			text, _ := root.SetNewText("k2")
			text.Edit(0, 0, builder.String())
			return nil
		}, "initial")
		assert.NoError(b, err)

		err = doc.Update(func(root *json.Object) error {
			text, _ := root.GetText("k2")
			for i := 0; i < cnt; i++ {
				if i != cnt {
					text.Edit(i, i+1, "b")
				}
			}
			return nil
		}, "Modify one node multiple times")
		assert.NoError(b, err)

		assert.Equal(b, cnt, doc.GarbageLen())
		gc, _ := doc.GarbageCollect(time.MaxTicket)
		assert.Equal(b, cnt, gc)
	}
}

func benchmarkArray(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			array, _ := root.SetNewArray("k1")
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
		err := doc.Update(func(root *json.Object) error {
			root.SetNewArray("1")
			for i := 0; i < cnt; i++ {
				arr, _ := root.GetArray("1")
				arr.AddInteger(i)
			}

			return nil
		}, "creates an array then adds integers")
		assert.NoError(b, err)

		err = doc.Update(func(root *json.Object) error {
			root.Delete("1")
			return nil
		}, "deletes the array")
		assert.NoError(b, err)

		gc, _ := doc.GarbageCollect(time.MaxTicket)
		assert.Equal(b, cnt+1, gc)
		assert.NoError(b, err)
	}
}

func benchmarkCounter(cnt int, b *testing.B) {
	for i := 0; i < b.N; i++ {
		doc := document.New("d1")

		err := doc.Update(func(root *json.Object) error {
			counter, _ := root.SetNewCounter("k1", crdt.IntegerCnt, 0)
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

		err := doc.Update(func(root *json.Object) error {
			for c := 0; c < cnt; c++ {
				root.SetInteger("k1", c)
			}
			return nil
		})
		assert.NoError(b, err)
	}
}
