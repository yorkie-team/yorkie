package document_test

import (
	"testing"

	"github.com/hackerwins/yorkie/pkg/document"
	"github.com/hackerwins/yorkie/pkg/document/checkpoint"
	"github.com/hackerwins/yorkie/pkg/document/proxy"

	"github.com/stretchr/testify/assert"
)

func TestDocument(t *testing.T) {
	t.Run("constructor test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		assert.Equal(t, doc.Checkpoint(), checkpoint.Initial)
		assert.False(t, doc.HasLocalChanges())
	})

	t.Run("equals test", func(t *testing.T) {
		doc1 := document.New("c1", "d1")
		doc2 := document.New("c1", "d2")
		doc3 := document.New("c1", "d3")

		if err := doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "updates k1"); err != nil {
			t.Error(err)
		}

		assert.False(t, doc1.Equals(doc2))
		assert.True(t, doc2.Equals(doc3))
	})

	t.Run("nested update test", func(t *testing.T) {
		expected := "{\"k1\":\"v1\",\"k2\":{\"k4\":\"v4\"},\"k3\":[\"v5\",\"v6\"]}"

		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5").AddString("v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3"); err != nil {
			t.Error(err)
		}

		assert.Equal(t, expected, doc.Marshal())
		assert.True(t, doc.HasLocalChanges())
	})

	t.Run("remove test", func(t *testing.T) {
		doc := document.New("c1", "d1")
		assert.Equal(t, "{}", doc.Marshal())
		assert.False(t, doc.HasLocalChanges())

		expected := "{\"k1\":\"v1\",\"k2\":{\"k4\":\"v4\"},\"k3\":[\"v5\",\"v6\"]}"
		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			root.SetNewObject("k2").SetString("k4", "v4")
			root.SetNewArray("k3").AddString("v5").AddString("v6")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "updates k1,k2,k3"); err != nil {
			t.Error(err)
		}
		assert.Equal(t, expected, doc.Marshal())

		expected = "{\"k1\":\"v1\",\"k3\":[\"v5\",\"v6\"]}"
		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k2")
			assert.Equal(t, expected, root.Marshal())
			return nil
		}, "removes k2"); err != nil {
			t.Error(err)
		}
		assert.Equal(t, expected, doc.Marshal())
	})

	t.Run("array test", func(t *testing.T) {
		doc := document.New("c1", "d1")

		if err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("1").AddString("2").AddString("3")
			assert.Equal(t, 3, root.GetArray("k1").Len())
			assert.Equal(t, "{\"k1\":[\"1\",\"2\",\"3\"]}", root.Marshal())

			root.GetArray("k1").Remove(1)
			assert.Equal(t, "{\"k1\":[\"1\",\"3\"]}", root.Marshal())
			assert.Equal(t, 2, root.GetArray("k1").Len())
			return nil
		}); err != nil {
			t.Error(err)
		}

	})
}
