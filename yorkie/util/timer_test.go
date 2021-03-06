package util_test

import (
	"testing"
	"time"

	"github.com/magiconair/properties/assert"

	"github.com/yorkie-team/yorkie/yorkie/util"
)

func TestTimer(t *testing.T) {
	t.Run("timer split test", func(t *testing.T) {
		timer := util.NewTimer()

		time.Sleep(time.Second * 1)
		assert.Equal(t, 1, int(timer.Split().Seconds()))
		time.Sleep(time.Second * 1)
		assert.Equal(t, 2, int(timer.Split().Seconds()))
		time.Sleep(time.Second * 1)
		assert.Equal(t, 3, int(timer.Split().Seconds()))
	})
}
