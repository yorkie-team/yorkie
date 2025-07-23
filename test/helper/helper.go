/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package helper provides helper functions for testing.
package helper

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/v2/bson"
	gomongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.uber.org/zap"

	adminClient "github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/internal/validation"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/index"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/rpc"
)

var testStartedAt int64
var logger *zap.Logger

// Below are the values of the Yorkie config used in the test.
var (
	RPCPort = 11101

	ProfilingPort = 11102

	AdminUser                             = server.DefaultAdminUser
	AdminPassword                         = server.DefaultAdminPassword
	AdminPasswordForSignUp                = AdminPassword + "123!"
	UseDefaultProject                     = true
	HousekeepingInterval                  = 10 * gotime.Second
	HousekeepingCandidatesLimitPerProject = 10
	HousekeepingProjectFetchSize          = 10
	HousekeepingCompactionMinChanges      = 1000

	AdminTokenDuration          = "10s"
	ClientDeactivateThreshold   = "10s"
	SnapshotThreshold           = int64(10)
	SnapshotCacheSize           = 10
	AuthWebhookMaxWaitInterval  = 3 * gotime.Millisecond
	AuthWebhookMinWaitInterval  = 3 * gotime.Millisecond
	AuthWebhookRequestTimeout   = 100 * gotime.Millisecond
	AuthWebhookSize             = 100
	AuthWebhookCacheTTL         = 10 * gotime.Second
	EventWebhookMaxWaitInterval = 3 * gotime.Millisecond
	EventWebhookMinWaitInterval = 3 * gotime.Millisecond
	EventWebhookRequestTimeout  = 100 * gotime.Millisecond
	EventWebhookSize            = 100
	EventWebhookCacheTTL        = 10 * gotime.Second
	ProjectCacheSize            = 256
	ProjectCacheTTL             = 5 * gotime.Second

	MongoConnectionURI     = "mongodb://localhost:27017"
	MongoConnectionTimeout = "5s"
	MongoPingTimeout       = "5s"
)

func init() {
	now := gotime.Now()
	testStartedAt = now.Unix()

	var err error
	logger, err = zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}
}

// TestDBName returns the name of test database with timestamp.
// timestamp is set only once on first call.
func TestDBName() string {
	return fmt.Sprintf("test-%s-%d", server.DefaultMongoYorkieDatabase, testStartedAt)
}

// CreateAdminCli returns a new instance of admin cli for testing.
func CreateAdminCli(t assert.TestingT, rpcAddr string) *adminClient.Client {
	adminCli, err := adminClient.Dial(rpcAddr, adminClient.WithInsecure(true))
	assert.NoError(t, err)

	_, err = adminCli.LogIn(context.Background(), server.DefaultAdminUser, server.DefaultAdminPassword)
	assert.NoError(t, err)

	return adminCli
}

// TestRoot returns the root
func TestRoot() *crdt.Root {
	return crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
}

// TextChangeContext returns the context of test change.
func TextChangeContext(root *crdt.Root) *change.Context {
	return change.NewContext(
		change.InitialID(),
		"",
		root,
	)
}

// PosT is a helper function that issues a new CRDTTreeNodeID.
func PosT(change *change.Context, offset ...int) *crdt.TreeNodeID {
	pos := &crdt.TreeNodeID{
		CreatedAt: change.IssueTimeTicket(),
		Offset:    0,
	}

	if len(offset) > 0 {
		pos.Offset = offset[0]
	}

	return pos
}

// TimeT is a helper function that issues a new TimeTicket
func TimeT(change *change.Context) *time.Ticket {
	return change.IssueTimeTicket()
}

// MaxVersionVector returns the max version vector of the given actors.
func MaxVersionVector(actors ...time.ActorID) time.VersionVector {
	if len(actors) == 0 {
		actors = append(actors, time.InitialActorID)
	}

	vector := time.NewVersionVector()
	for i := range len(actors) {
		vector.Set(actors[i], time.MaxLamport)
	}

	return vector
}

// VersionVectorOf creates a new version vector from the given actors.
func VersionVectorOf(actors map[time.ActorID]int64) time.VersionVector {
	vector := time.NewVersionVector()
	for actor, lamport := range actors {
		vector.Set(actor, lamport)
	}
	return vector
}

// TokensEqualBetween is a helper function that checks the tokens between the given
// indexes.
func TokensEqualBetween(t assert.TestingT, tree *index.Tree[*crdt.TreeNode], from, to int, expected []string) bool {
	var nodes []*crdt.TreeNode
	var tokenTypes []index.TokenType
	err := tree.TokensBetween(from, to, func(token index.TreeToken[*crdt.TreeNode], _ bool) {
		nodes = append(nodes, token.Node)
		tokenTypes = append(tokenTypes, token.TokenType)
	})
	assert.NoError(t, err)

	var actual []string
	for i := range len(nodes) {
		actual = append(actual, fmt.Sprintf("%s:%s", ToDiagnostic(nodes[i]), tokenTypes[i].ToString()))
	}
	assert.Equal(t, expected, actual)

	return true
}

// ToDiagnostic is a helper function that converts the given node to a
// diagnostic string.
func ToDiagnostic(node *crdt.TreeNode) string {
	if node.IsText() {
		return node.Value
	}

	return node.Type()
}

// BuildIndexTree builds an index tree from the given block node.
func BuildIndexTree(node json.TreeNode) *index.Tree[*crdt.TreeNode] {
	doc := document.New("test")
	err := doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("test", node)

		return nil
	})
	if err != nil {
		return nil
	}

	return doc.Root().GetTree("test").IndexTree
}

// BuildTreeNode builds a crdt.TreeNode from the given tree node.
func BuildTreeNode(node *json.TreeNode) *crdt.TreeNode {
	doc := document.New("test")
	err := doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("test", *node)

		return nil
	})
	if err != nil {
		return nil
	}

	return doc.Root().GetTree("test").Root()
}

type treeNodePair struct {
	node     *crdt.TreeNode
	parentID *crdt.TreeNodeID
}

func createTreeNodePairs(node *crdt.TreeNode, parentID *crdt.TreeNodeID) []treeNodePair {
	var pairs []treeNodePair

	pairs = append(pairs, treeNodePair{node, parentID})
	for _, child := range node.Index.Children(true) {
		pairs = append(pairs, createTreeNodePairs(child.Value, node.ID())...)
	}
	return pairs
}

// AssertEqualTreeNode asserts that the given TreeNodes are equal.
func AssertEqualTreeNode(t *testing.T, nodeA, nodeB *crdt.TreeNode) {
	pairsA := createTreeNodePairs(nodeA, nil)
	pairsB := createTreeNodePairs(nodeB, nil)
	assert.Equal(t, pairsA, pairsB)
}

var portOffset = 0

// TestConfig returns config for creating Yorkie instance.
func TestConfig() *server.Config {
	portOffset += 100
	return &server.Config{
		RPC: &rpc.Config{
			Port: RPCPort + portOffset,
		},
		Profiling: &profiling.Config{
			Port: ProfilingPort + portOffset,
		},
		Housekeeping: &housekeeping.Config{
			Interval:                  HousekeepingInterval.String(),
			CandidatesLimitPerProject: HousekeepingCandidatesLimitPerProject,
			ProjectFetchSize:          HousekeepingProjectFetchSize,
			CompactionMinChanges:      HousekeepingCompactionMinChanges,
		},
		Backend: &backend.Config{
			AdminUser:                   server.DefaultAdminUser,
			AdminPassword:               server.DefaultAdminPassword,
			SecretKey:                   server.DefaultSecretKey,
			AdminTokenDuration:          server.DefaultAdminTokenDuration.String(),
			UseDefaultProject:           true,
			ClientDeactivateThreshold:   server.DefaultClientDeactivateThreshold,
			SnapshotInterval:            10,
			SnapshotThreshold:           SnapshotThreshold,
			SnapshotCacheSize:           SnapshotCacheSize,
			AuthWebhookMaxWaitInterval:  AuthWebhookMaxWaitInterval.String(),
			AuthWebhookMinWaitInterval:  AuthWebhookMinWaitInterval.String(),
			AuthWebhookRequestTimeout:   AuthWebhookRequestTimeout.String(),
			AuthWebhookCacheSize:        AuthWebhookSize,
			AuthWebhookCacheTTL:         AuthWebhookCacheTTL.String(),
			EventWebhookMaxWaitInterval: EventWebhookMaxWaitInterval.String(),
			EventWebhookMinWaitInterval: EventWebhookMinWaitInterval.String(),
			EventWebhookRequestTimeout:  EventWebhookRequestTimeout.String(),
			ProjectCacheSize:            ProjectCacheSize,
			ProjectCacheTTL:             ProjectCacheTTL.String(),
			GatewayAddr:                 fmt.Sprintf("localhost:%d", RPCPort+portOffset),
		},
		Mongo: &mongo.Config{
			ConnectionURI:     MongoConnectionURI,
			ConnectionTimeout: MongoConnectionTimeout,
			PingTimeout:       MongoPingTimeout,
			YorkieDatabase:    TestDBName(),
		},
	}
}

// TestServer returns a new instance of Yorkie for testing.
func TestServer() *server.Yorkie {
	y, err := server.New(TestConfig())
	if err != nil {
		log.Fatal(err)
	}
	return y
}

// TestServerWithSnapshotCfg returns a new instance of Yorkie for testing with the
// given snapshot interval and threshold.
func TestServerWithSnapshotCfg(snapshotInterval int64, snapshotThreshold int64) (*server.Yorkie, error) {
	config := TestConfig()
	config.Backend.SnapshotInterval = snapshotInterval
	config.Backend.SnapshotThreshold = snapshotThreshold

	svr, err := server.New(config)
	if err != nil {
		return nil, err
	}
	if err := svr.Start(); err != nil {
		return nil, err
	}
	return svr, nil
}

// TestDocKey returns a new instance of document key for testing.
func TestDocKey(t testing.TB, prefix ...int) key.Key {
	name := t.Name()

	if len(prefix) > 0 {
		name = fmt.Sprintf("%d-%s", prefix[0], name)
	}

	if err := key.Key(name).Validate(); err == nil {
		return key.Key(name)
	}

	if len(name) > 100 {
		name = name[:100]
	}

	sb := strings.Builder{}
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			sb.WriteRune(c + ('a' - 'A'))
		} else if c >= 'a' && c <= 'z' {
			sb.WriteRune(c)
		} else if c >= '0' && c <= '9' {
			sb.WriteRune(c)
		} else {
			sb.WriteRune('-')
		}
	}

	return key.Key(sb.String())
}

// TestSlugName returns a new instance of slug name for testing.
func TestSlugName(t testing.TB) string {
	name := t.Name()
	if err := validation.Validate(name, []any{
		"required",
		"min=4",
		"max=30",
		"slug",
	}); err == nil {
		return name
	}

	if len(name) > 35 {
		name = name[len(name)-30:]
	}

	sb := strings.Builder{}
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			sb.WriteRune(c + ('a' - 'A'))
		} else if c >= 'a' && c <= 'z' {
			sb.WriteRune(c)
		} else if c >= '0' && c <= '9' {
			sb.WriteRune(c)
		} else {
			sb.WriteRune('-')
		}
	}

	return sb.String()
}

// NewRangeSlice returns a slice of integers from start to end.
func NewRangeSlice(start, end int) []int {
	var slice []int
	if start < end {
		for i := start; i <= end; i++ {
			slice = append(slice, i)
		}
		return slice
	}

	for i := start; i >= end; i-- {
		slice = append(slice, i)
	}
	return slice
}

// setupRawMongoClient returns the raw mongo client.
func setupRawMongoClient(databaseName string) (*gomongo.Client, error) {
	conf := &mongo.Config{
		ConnectionTimeout: "5s",
		ConnectionURI:     "mongodb://localhost:27017",
		YorkieDatabase:    databaseName,
		PingTimeout:       "5s",
	}

	ctx, cancel := context.WithTimeout(context.Background(), conf.ParseConnectionTimeout())
	defer cancel()

	client, err := gomongo.Connect(
		options.Client().
			ApplyURI(conf.ConnectionURI).
			SetRegistry(mongo.NewRegistryBuilder()),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to mongo: %w", err)
	}

	pingTimeout := conf.ParsePingTimeout()
	ctxPing, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := client.Ping(ctxPing, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("ping mongo: %w", err)
	}

	logging.DefaultLogger().Infof("MongoDB connected, URI: %s, DB: %s", conf.ConnectionURI, conf.YorkieDatabase)

	return client, nil
}

// CleanUpAllCollections removes all data in every collection.
func CleanUpAllCollections(databaseName string) error {
	cli, err := setupRawMongoClient(databaseName)
	if err != nil {
		return err
	}

	for _, col := range mongo.Collections {
		_, err := cli.Database(databaseName).Collection(col).DeleteMany(context.Background(), bson.D{})
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateDummyDocumentWithID creates a new dummy document with the given ID and key.
func CreateDummyDocumentWithID(
	databaseName string,
	projectID types.ID,
	docID types.ID,
	docKey key.Key,
) error {
	cli, err := setupRawMongoClient(databaseName)
	if err != nil {
		return err
	}
	_, err = cli.Database(databaseName).Collection(mongo.ColDocuments).InsertOne(
		context.Background(),
		bson.M{
			"_id":        docID,
			"project_id": projectID,
			"key":        docKey,
		},
	)
	if err != nil {
		return err
	}

	return nil
}

// FindDocInfosWithID finds the docInfos of the given projectID and docID.
func FindDocInfosWithID(
	databaseName string,
	docID types.ID,
) ([]*database.DocInfo, error) {
	ctx := context.Background()
	cli, err := setupRawMongoClient(databaseName)
	if err != nil {
		return nil, err
	}

	cursor, err := cli.Database(databaseName).Collection(mongo.ColDocuments).Find(
		ctx,
		bson.M{
			"_id": docID,
		}, options.Find())
	if err != nil {
		return nil, err
	}

	var infos []*database.DocInfo
	if err := cursor.All(ctx, &infos); err != nil {
		return nil, err
	}

	return infos, nil
}

// CreateDummyClientWithID creates a new dummy document with the given ID and key.
func CreateDummyClientWithID(
	databaseName string,
	projectID types.ID,
	clientKey string,
	clientID types.ID,
) error {
	cli, err := setupRawMongoClient(databaseName)
	if err != nil {
		return err
	}
	_, err = cli.Database(databaseName).Collection(mongo.ColClients).InsertOne(
		context.Background(),
		bson.M{
			"_id":        clientID,
			"project_id": projectID,
			"key":        clientKey,
		},
	)
	if err != nil {
		return err
	}

	return nil
}

// WaitForServerToStart waits for the server to start.
func WaitForServerToStart(addr string) error {
	maxRetries := 10
	initialDelay := 100 * gotime.Millisecond
	maxDelay := 5 * gotime.Second

	for attempt := range maxRetries {
		// Exponential backoff calculation
		delay := initialDelay * gotime.Duration(1<<uint(attempt))
		fmt.Println("delay: ", delay)
		delay = min(delay, maxDelay)

		conn, err := net.DialTimeout("tcp", addr, 1*gotime.Second)
		if err != nil {
			gotime.Sleep(delay)
			continue
		}

		err = conn.Close()
		if err != nil {
			return fmt.Errorf("close connection: %w", err)
		}

		return nil
	}

	return fmt.Errorf("timeout for server to start: %s", addr)
}

// CreateProjectAndDocuments creates a new project and documents for the given count.
func CreateProjectAndDocuments(t *testing.T, server *server.Yorkie, count int) (*types.Project, []*document.Document) {
	ctx := context.Background()
	project, err := server.CreateProject(ctx, t.Name())
	assert.NoError(t, err)

	cli, err := client.Dial(server.RPCAddr(), client.WithAPIKey(project.PublicKey))
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(ctx))

	var docs []*document.Document
	for i := range count {
		doc := document.New(TestDocKey(t, i))
		assert.NoError(t, cli.Attach(ctx, doc))
		docs = append(docs, doc)
	}

	return project, docs
}

// ClientAndAttachedDoc creates a new client and attaches it to a new document.
func ClientAndAttachedDoc(
	ctx context.Context,
	rpcAddr string,
	docKey key.Key,
) (*client.Client, *document.Document, error) {
	c, err := client.Dial(rpcAddr, client.WithLogger(logger))
	if err != nil {
		return nil, nil, err
	}
	if err := c.Activate(ctx); err != nil {
		return nil, nil, err
	}
	d := document.New(docKey)
	if err := c.Attach(ctx, d); err != nil {
		return nil, nil, err
	}
	return c, d, nil
}

// ClientsAndAttachedDocs creates n clients and attaches them to a new document.
func ClientsAndAttachedDocs(
	ctx context.Context,
	rpcAddr string,
	docKey key.Key,
	n int,
) ([]*client.Client, []*document.Document, error) {
	var clients []*client.Client
	var docs []*document.Document

	for range n {
		c, doc, err := ClientAndAttachedDoc(ctx, rpcAddr, docKey)
		if err != nil {
			return nil, nil, err
		}

		clients = append(clients, c)
		docs = append(docs, doc)
	}
	return clients, docs, nil
}

// ActiveClients is a helper function to create n active clients.
func ActiveClients(b *testing.B, rpcAddr string, n int) (clients []*client.Client) {
	for range n {
		c, err := client.Dial(
			rpcAddr,
			client.WithMaxRecvMsgSize(50*1024*1024),
			client.WithLogger(logger),
		)
		assert.NoError(b, err)

		assert.NoError(b, c.Activate(context.Background()))
		clients = append(clients, c)
	}
	return
}

// CleanupClients is a helper function to clean up clients.
func CleanupClients(b *testing.B, clients []*client.Client) {
	for _, c := range clients {
		assert.NoError(b, c.Deactivate(context.Background()))
		assert.NoError(b, c.Close())
	}
}

// VerifySignature verifies that the HMAC signature in the header matches the expected value.
func VerifySignature(signatureHeader, secret string, body []byte) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	expectedSigHeader := fmt.Sprintf("sha256=%s", expectedSig)
	if !hmac.Equal([]byte(signatureHeader), []byte(expectedSigHeader)) {
		return errors.New("signature validation failed")
	}
	return nil
}
