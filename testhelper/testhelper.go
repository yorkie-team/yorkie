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
