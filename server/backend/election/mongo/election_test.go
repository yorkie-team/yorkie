package mongo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	mongoelection "github.com/yorkie-team/yorkie/server/backend/election/mongo"
	"github.com/yorkie-team/yorkie/test/helper"
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
		time.Sleep(helper.LeaseDuration)

		assert.NoError(t, electorB.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))
		assert.NoError(t, electorC.StartElection(leaseLockName, helper.LeaseDuration, normalTask, stopTask))
		time.Sleep(helper.LeaseDuration)

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
			time.Sleep(helper.LeaseDuration * 4)
		}

		electorA := mongoelection.NewElector("A", db)
		electorB := mongoelection.NewElector("B", db)
		electorC := mongoelection.NewElector("C", db)

		assert.NoError(t, electorA.StartElection(leaseLockName, helper.LeaseDuration, longTask, stopTask))
		time.Sleep(helper.LeaseDuration)

		assert.NoError(t, electorB.StartElection(leaseLockName, helper.LeaseDuration, longTask, stopTask))
		assert.NoError(t, electorC.StartElection(leaseLockName, helper.LeaseDuration, longTask, stopTask))

		// wait for lease expiration and check if elector A is still the leader while handling a long task
		time.Sleep(helper.LeaseDuration)

		leader, err := db.FindLeader(context.Background(), leaseLockName)
		assert.NoError(t, err)

		assert.Equal(t, "A", *leader)
	})

	t.Run("handle background routines when shutting down the server test", func(t *testing.T) {
		shutdownCh := make(chan struct{})

		isTaskDone := false
		longTask := func(ctx context.Context) {
			close(shutdownCh)
			time.Sleep(helper.LeaseDuration)
			isTaskDone = true
		}

		elector := mongoelection.NewElector("A", db)
		assert.NoError(t, elector.StartElection(t.Name(), helper.LeaseDuration, longTask, stopTask))

		// if receive shutdown signal, stop elector
		select {
		case <-shutdownCh:
			assert.NoError(t, elector.Stop())
		}

		// check if the task is done
		// this means that the background routine is handled properly after server(elector) is stopped
		assert.Equal(t, true, isTaskDone)
	})
}
