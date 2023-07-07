package mongo_test

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	mongoelection "github.com/yorkie-team/yorkie/server/backend/election/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
	"testing"
	"time"
)

var (
	normalTask = func(ctx context.Context) {}
	stopTask   = func() {}
)

func setupTestWithDummyData(t *testing.T) *mongo.Client {
	config := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    helper.TestDBName(),
		PingTimeout:       "5s",
	}
	assert.NoError(t, config.Validate())

	db, err := mongo.Dial(config)
	assert.NoError(t, err)

	return db
}

func TestElection(t *testing.T) {
	db := setupTestWithDummyData(t)

	t.Run("leader election with multiple electors test", func(t *testing.T) {
		leaseLockName := t.Name()

		electorA := mongoelection.NewElector("A", db)
		electorB := mongoelection.NewElector("B", db)
		electorC := mongoelection.NewElector("C", db)

		assert.NoError(t, electorA.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))
		assert.NoError(t, electorB.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))
		assert.NoError(t, electorC.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))

		// elector A will be the leader because it is the first to start the election.
		leader, err := db.FindLeader(context.Background(), leaseLockName)
		assert.NoError(t, err)

		assert.Equal(t, "A", *leader)

		// wait for lease expiration and check the leader again
		// elector A is still the leader because it has renewed the lease.
		time.Sleep(helper.LeaseDuration)
		assert.Equal(t, "A", *leader)

		// stop electorA and electorB, then wait for the next leader election
		// elector C will be the leader because other electors are stopped.
		assert.NoError(t, electorA.Stop())
		assert.NoError(t, electorB.Stop())

		time.Sleep(helper.LeaseDuration)

		leader, err = db.FindLeader(context.Background(), leaseLockName)
		assert.NoError(t, err)

		assert.Equal(t, "C", *leader)
	})

	t.Run("lease renewal while handling a a long task test", func(t *testing.T) {
		leaseLockName := t.Name()
		longTask := func(ctx context.Context) {
			time.Sleep(helper.LeaseDuration * 2)
		}

		electorA := mongoelection.NewElector("A", db)
		electorB := mongoelection.NewElector("B", db)
		electorC := mongoelection.NewElector("C", db)

		assert.NoError(t, electorA.StartElection(leaseLockName, helper.LeaseDuration, longTask, stopTask))
		assert.NoError(t, electorB.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))
		assert.NoError(t, electorC.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))

		// check if elector A is still the leader
		time.Sleep(helper.LeaseDuration)

		leader, err := db.FindLeader(context.Background(), leaseLockName)
		assert.NoError(t, err)

		assert.Equal(t, "A", *leader)
	})

	t.Run("handle background routines when shutting down the server test", func(t *testing.T) {
		// TODO(krapie): find the way to gradually close election routines
		t.Skip()
	})
}
