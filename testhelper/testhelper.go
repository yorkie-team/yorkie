package testhelper

import (
	"testing"

	"github.com/hackerwins/rottie/rottie"
	"github.com/hackerwins/rottie/rottie/backend/mongo"
)

var (
	testConfig = &rottie.Config{
		RPCPort: 1101,
		Mongo: &mongo.Config{
			ConnectionURI:        "mongodb://localhost:27017",
			ConnectionTimeoutSec: 5,
			PingTimeoutSec:       5,
			RottieDatabase:       "rottie-meta",
		},
	}
)

func WithRottie(t *testing.T, f func(*testing.T, *rottie.Rottie)) {
	r, err := rottie.New(testConfig)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Start(); err != nil {
		t.Fatal(err)
	}

	f(t, r)

	if err := r.Shutdown(true); err != nil {
		t.Error(err)
	}
}
