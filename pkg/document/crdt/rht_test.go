package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarshal(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		key1 := `hello\\\t`
		value1 := "world\"\f\b"
		key2 := "hi"
		value2 := `test\r`
		expected := `{"hello\\\\\\t":"world\\"\\f\\b","hi":"test\\r"}`

		rht := NewRHT()
		rht.Set(key1, value1, nil)
		rht.Set(key2, value2, nil)
		actual := rht.Marshal()
		assert.Equal(t, expected, actual)
	})
}
