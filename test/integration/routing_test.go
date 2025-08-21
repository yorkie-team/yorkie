package integration

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/test/helper"
)

// attachWithRetry tries Attach with small backoff on FailedPrecondition(conflict)
func attachWithRetry(ctx context.Context, c *client.Client, d *document.Document, tries int) error {
	var lastErr error
	for i := 0; i < tries; i++ {
		if err := c.Attach(ctx, d); err != nil {
			lastErr = err
			if connect.CodeOf(err) == connect.CodeFailedPrecondition {
				time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func TestClientRouting(t *testing.T) {
	ctx := context.Background()

	const clientKey = "C1-routing-test"
	docKey := helper.TestDocKey(t)

	// Start two servers A and B sharing the same DB.
	svrA := helper.TestServer()
	assert.NoError(t, svrA.Start())
	defer func() { _ = svrA.Shutdown(true) }()

	svrB := helper.TestServer()
	assert.NoError(t, svrB.Start())
	defer func() { _ = svrB.Shutdown(true) }()

	// t0: Connect to A with fixed client key
	cliA, err := client.Dial(svrA.RPCAddr(), client.WithKey(clientKey))
	assert.NoError(t, err)
	assert.NoError(t, cliA.Activate(ctx))

	docA := document.New(docKey)
	assert.NoError(t, cliA.Attach(ctx, docA))

	for i := 0; i < 2; i++ {
		err := docA.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k", "vA")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, cliA.Sync(ctx))
	}
	// Ensure the server has the latest state from A
	assert.NoError(t, cliA.Detach(ctx, docA))
	// Small delay to reduce cross-server write races
	time.Sleep(150 * time.Millisecond)

	// t1: Connect to B with SAME client key
	cliB, err := client.Dial(svrB.RPCAddr(), client.WithKey(clientKey))
	assert.NoError(t, err)
	assert.NoError(t, cliB.Activate(ctx))
	docB := document.New(docKey)
	assert.NoError(t, attachWithRetry(ctx, cliB, docB, 5))
	assert.NoError(t, cliB.Sync(ctx))
	gotB := docB.Marshal()
	assert.Contains(t, gotB, "\"k\":\"vA\"")

	for i := 0; i < 2; i++ {
		err := docB.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k", "vB")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, cliB.Sync(ctx))
	}
	assert.NoError(t, cliB.Detach(ctx, docB))
	// Small delay to reduce cross-server write races
	time.Sleep(150 * time.Millisecond)

	// t2: Reconnect to A with SAME client key
	cliA2, err := client.Dial(svrA.RPCAddr(), client.WithKey(clientKey))
	assert.NoError(t, err)
	assert.NoError(t, cliA2.Activate(ctx))
	docA2 := document.New(docKey)
	assert.NoError(t, attachWithRetry(ctx, cliA2, docA2, 5))
	assert.NoError(t, cliA2.Sync(ctx))
	gotA2 := docA2.Marshal()
	assert.Contains(t, gotA2, "\"k\":\"vB\"")

	err = docA2.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetString("k", "vA2")
		return nil
	})
	assert.NoError(t, err)
	assert.NoError(t, cliA2.Sync(ctx))
}

func TestClusterRoutingWithMongoDB(t *testing.T) {
	ctx := context.Background()
	const clientKey = "C1-cluster-routing-test"

	// Create two backends sharing the same MongoDB
	metA, err := prometheus.NewMetrics()
	assert.NoError(t, err)

	backendA, err := backend.New(
		&backend.Config{
			AdminUser:                   helper.AdminUser,
			AdminPassword:               helper.AdminPassword,
			UseDefaultProject:           helper.UseDefaultProject,
			ClientDeactivateThreshold:   helper.ClientDeactivateThreshold,
			SnapshotThreshold:           helper.SnapshotThreshold,
			SnapshotCacheSize:           helper.SnapshotCacheSize,
			AuthWebhookCacheSize:        helper.AuthWebhookSize,
			AuthWebhookCacheTTL:         helper.AuthWebhookCacheTTL.String(),
			AuthWebhookMaxWaitInterval:  helper.AuthWebhookMaxWaitInterval.String(),
			AuthWebhookMinWaitInterval:  helper.AuthWebhookMinWaitInterval.String(),
			AuthWebhookRequestTimeout:   helper.AuthWebhookRequestTimeout.String(),
			EventWebhookMaxWaitInterval: helper.EventWebhookMaxWaitInterval.String(),
			EventWebhookMinWaitInterval: helper.EventWebhookMinWaitInterval.String(),
			EventWebhookRequestTimeout:  helper.EventWebhookRequestTimeout.String(),
			ProjectCacheSize:            helper.ProjectCacheSize,
			ProjectCacheTTL:             helper.ProjectCacheTTL.String(),
			AdminTokenDuration:          helper.AdminTokenDuration,
		}, &mongo.Config{
			ConnectionURI:     helper.MongoConnectionURI,
			YorkieDatabase:    helper.TestDBName(),
			ConnectionTimeout: helper.MongoConnectionTimeout,
			PingTimeout:       helper.MongoPingTimeout,
		}, &housekeeping.Config{
			Interval:                  helper.HousekeepingInterval.String(),
			CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
			ProjectFetchSize:          helper.HousekeepingProjectFetchSize,
		}, metA, nil, nil)
	assert.NoError(t, err)
	defer func() { _ = backendA.Shutdown() }()

	metB, err := prometheus.NewMetrics()
	assert.NoError(t, err)

	backendB, err := backend.New(
		&backend.Config{
			AdminUser:                   helper.AdminUser,
			AdminPassword:               helper.AdminPassword,
			UseDefaultProject:           helper.UseDefaultProject,
			ClientDeactivateThreshold:   helper.ClientDeactivateThreshold,
			SnapshotThreshold:           helper.SnapshotThreshold,
			SnapshotCacheSize:           helper.SnapshotCacheSize,
			AuthWebhookCacheSize:        helper.AuthWebhookSize,
			AuthWebhookCacheTTL:         helper.AuthWebhookCacheTTL.String(),
			AuthWebhookMaxWaitInterval:  helper.AuthWebhookMaxWaitInterval.String(),
			AuthWebhookMinWaitInterval:  helper.AuthWebhookMinWaitInterval.String(),
			AuthWebhookRequestTimeout:   helper.AuthWebhookRequestTimeout.String(),
			EventWebhookMaxWaitInterval: helper.EventWebhookMaxWaitInterval.String(),
			EventWebhookMinWaitInterval: helper.EventWebhookMinWaitInterval.String(),
			EventWebhookRequestTimeout:  helper.EventWebhookRequestTimeout.String(),
			ProjectCacheSize:            helper.ProjectCacheSize,
			ProjectCacheTTL:             helper.ProjectCacheTTL.String(),
			AdminTokenDuration:          helper.AdminTokenDuration,
		}, &mongo.Config{
			ConnectionURI:     helper.MongoConnectionURI,
			YorkieDatabase:    helper.TestDBName(),
			ConnectionTimeout: helper.MongoConnectionTimeout,
			PingTimeout:       helper.MongoPingTimeout,
		}, &housekeeping.Config{
			Interval:                  helper.HousekeepingInterval.String(),
			CandidatesLimitPerProject: helper.HousekeepingCandidatesLimitPerProject,
			ProjectFetchSize:          helper.HousekeepingProjectFetchSize,
		}, metB, nil, nil)
	assert.NoError(t, err)
	defer func() { _ = backendB.Shutdown() }()

	// Start both backends
	assert.NoError(t, backendA.Start(ctx))
	assert.NoError(t, backendB.Start(ctx))

	// Get project info
	projectInfo, err := backendA.DB.FindProjectInfoByID(ctx, database.DefaultProjectID)
	assert.NoError(t, err)
	project := projectInfo.ToProject()

	t.Run("client routing with cache behavior", func(t *testing.T) {
		// t0: Activate client on backend A
		clientInfoA, err := backendA.DB.ActivateClient(ctx, project.ID, clientKey, map[string]string{})
		assert.NoError(t, err)
		assert.Equal(t, database.ClientActivated, clientInfoA.Status)

		// t1: Check if backend B can see the activated client (cache miss -> DB lookup)
		clientInfoB, err := backendB.DB.FindClientInfoByRefKey(ctx, clientInfoA.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, database.ClientActivated, clientInfoB.Status)
		assert.Equal(t, clientInfoA.ID, clientInfoB.ID)

		// t2: Deactivate client on backend A
		_, err = backendA.DB.DeactivateClient(ctx, clientInfoA.RefKey())
		assert.NoError(t, err)
		// Small delay to work ttl cleanup
		time.Sleep(11 * time.Second)

		// t3: Check if backend B can see the deactivated client (cache miss -> DB lookup)
		clientInfoBAfterDeactivate, err := backendB.DB.FindClientInfoByRefKey(ctx, clientInfoA.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, database.ClientDeactivated, clientInfoBAfterDeactivate.Status)

		// t4: Try to activate same client key on backend B (should work since client is deactivated)
		clientInfoB2, err := backendB.DB.ActivateClient(ctx, project.ID, clientKey, map[string]string{})
		assert.NoError(t, err)
		assert.Equal(t, database.ClientActivated, clientInfoB2.Status)
		assert.Equal(t, clientInfoA.ID, clientInfoB2.ID, "Same client key should result in same client ID")
	})

	t.Run("client routing with document attachment", func(t *testing.T) {
		docKey := helper.TestDocKey(t)

		// t0: Activate client on backend A
		clientInfoA, err := backendA.DB.ActivateClient(ctx, project.ID, clientKey+"-doc", map[string]string{})
		assert.NoError(t, err)

		// t1: Create document on backend A
		docInfoA, err := backendA.DB.FindOrCreateDocInfo(ctx, clientInfoA.RefKey(), docKey)
		assert.NoError(t, err)

		// t2: Attach document on backend A
		clientInfoA, err = backendA.DB.TryAttaching(ctx, clientInfoA.RefKey(), docInfoA.ID)
		assert.NoError(t, err)
		// Small delay to work ttl cleanup
		time.Sleep(11 * time.Second)

		// t3: Check if backend B can see the attached document (cache miss -> DB lookup)
		clientInfoB, err := backendB.DB.FindClientInfoByRefKey(ctx, clientInfoA.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, database.DocumentAttaching, clientInfoB.Documents[docInfoA.ID].Status)
	})

	t.Run("client routing with housekeeping deactivation", func(t *testing.T) {
		// t0: Activate client on backend A
		clientInfoA, err := backendA.DB.ActivateClient(ctx, project.ID, clientKey+"-housekeeping", map[string]string{})
		assert.NoError(t, err)
		assert.Equal(t, database.ClientActivated, clientInfoA.Status)

		// t1: Deactivate client using housekeeping on backend B
		_, err = backendB.DB.DeactivateClientForHousekeeping(ctx, clientInfoA.RefKey())
		assert.NoError(t, err)
		// Small delay to work ttl cleanup
		time.Sleep(11 * time.Second)

		// t2: Check if backend A can see the deactivated client (cache miss -> DB lookup)
		clientInfoAAfterHousekeeping, err := backendA.DB.FindClientInfoByRefKey(ctx, clientInfoA.RefKey())
		assert.NoError(t, err)
		assert.Equal(t, database.ClientDeactivated, clientInfoAAfterHousekeeping.Status)

		// t3: Try to activate again on backend A (should work since client is deactivated)
		clientInfoA2, err := backendA.DB.ActivateClient(ctx, project.ID, clientKey+"-housekeeping", map[string]string{})
		assert.NoError(t, err)
		assert.Equal(t, database.ClientActivated, clientInfoA2.Status)
		assert.Equal(t, clientInfoA.ID, clientInfoA2.ID)
	})
}
