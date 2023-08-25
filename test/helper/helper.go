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
	"fmt"
	"log"
	"strings"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	adminClient "github.com/yorkie-team/yorkie/admin"
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
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/profiling"
	"github.com/yorkie-team/yorkie/server/rpc"
)

var testStartedAt int64

// Below are the values of the Yorkie config used in the test.
var (
	RPCPort                  = 21101
	RPCMaxRequestBytes       = uint64(4 * 1024 * 1024)
	RPCMaxConnectionAge      = 8 * gotime.Second
	RPCMaxConnectionAgeGrace = 2 * gotime.Second

	ProfilingPort = 21102

	AdminUser                             = server.DefaultAdminUser
	AdminPassword                         = server.DefaultAdminPassword
	HousekeepingInterval                  = 10 * gotime.Second
	HousekeepingCandidatesLimitPerProject = 10
	HousekeepingProjectFetchSize          = 10

	AdminTokenDuration         = "10s"
	ClientDeactivateThreshold  = "10s"
	SnapshotThreshold          = int64(10)
	SnapshotWithPurgingChanges = false
	AuthWebhookMaxWaitInterval = 3 * gotime.Millisecond
	AuthWebhookSize            = 100
	AuthWebhookCacheAuthTTL    = 10 * gotime.Second
	AuthWebhookCacheUnauthTTL  = 10 * gotime.Second
	ProjectInfoCacheSize       = 256
	ProjectInfoCacheTTL        = 5 * gotime.Second

	MongoConnectionURI     = "mongodb://localhost:27017"
	MongoConnectionTimeout = "5s"
	MongoPingTimeout       = "5s"
)

func init() {
	now := gotime.Now()
	testStartedAt = now.Unix()
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
		change.InitialID,
		"",
		root,
	)
}

// IssuePos is a helper function that issues a new CRDTTreeNodeID.
func IssuePos(change *change.Context, offset ...int) *crdt.TreeNodeID {
	pos := &crdt.TreeNodeID{
		CreatedAt: change.IssueTimeTicket(),
		Offset:    0,
	}

	if len(offset) > 0 {
		pos.Offset = offset[0]
	}

	return pos
}

// IssueTime is a helper function that issues a new TimeTicket
func IssueTime(change *change.Context) *time.Ticket {
	return change.IssueTimeTicket()
}

// NodesBetweenEqual is a helper function that checks the nodes between the given
// indexes.
func NodesBetweenEqual(t assert.TestingT, tree *index.Tree[*crdt.TreeNode], from, to int, expected []string) bool {
	var nodes []*crdt.TreeNode
	var contains []index.TagContained
	err := tree.NodesBetween(from, to, func(node *crdt.TreeNode, contain index.TagContained) {
		nodes = append(nodes, node)
		contains = append(contains, contain)
	})
	assert.NoError(t, err)

	var actual []string
	for i := 0; i < len(nodes); i++ {
		actual = append(actual, fmt.Sprintf("%s:%s", ToDiagnostic(nodes[i]), contains[i].ToString()))
	}
	assert.Equal(t, expected, actual)

	return true
}

// ToDiagnostic is a helper function that converts the given node to a
// diagnostic string.
func ToDiagnostic(node *crdt.TreeNode) string {
	if node.IsText() {
		return fmt.Sprintf("%s.%s", node.Type(), node.Value)
	}

	return node.Type()
}

// BuildIndexTree builds an index tree from the given block node.
func BuildIndexTree(node *json.TreeNode) *index.Tree[*crdt.TreeNode] {
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
		root.SetNewTree("test", node)

		return nil
	})
	if err != nil {
		return nil
	}

	return doc.Root().GetTree("test").Root()
}

var portOffset = 0

// TestConfig returns config for creating Yorkie instance.
func TestConfig() *server.Config {
	portOffset += 100
	return &server.Config{
		RPC: &rpc.Config{
			Port:                  RPCPort + portOffset,
			MaxRequestBytes:       RPCMaxRequestBytes,
			MaxConnectionAge:      RPCMaxConnectionAge.String(),
			MaxConnectionAgeGrace: RPCMaxConnectionAgeGrace.String(),
		},
		Profiling: &profiling.Config{
			Port: ProfilingPort + portOffset,
		},
		Housekeeping: &housekeeping.Config{
			Interval:                  HousekeepingInterval.String(),
			CandidatesLimitPerProject: HousekeepingCandidatesLimitPerProject,
			ProjectFetchSize:          HousekeepingProjectFetchSize,
		},
		Backend: &backend.Config{
			AdminUser:                  server.DefaultAdminUser,
			AdminPassword:              server.DefaultAdminPassword,
			SecretKey:                  server.DefaultSecretKey,
			AdminTokenDuration:         server.DefaultAdminTokenDuration.String(),
			UseDefaultProject:          true,
			ClientDeactivateThreshold:  server.DefaultClientDeactivateThreshold,
			SnapshotInterval:           10,
			SnapshotThreshold:          SnapshotThreshold,
			SnapshotWithPurgingChanges: SnapshotWithPurgingChanges,
			AuthWebhookMaxWaitInterval: AuthWebhookMaxWaitInterval.String(),
			AuthWebhookCacheSize:       AuthWebhookSize,
			AuthWebhookCacheAuthTTL:    AuthWebhookCacheAuthTTL.String(),
			AuthWebhookCacheUnauthTTL:  AuthWebhookCacheUnauthTTL.String(),
			ProjectInfoCacheSize:       ProjectInfoCacheSize,
			ProjectInfoCacheTTL:        ProjectInfoCacheTTL.String(),
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

// TestDocKey returns a new instance of document key for testing.
func TestDocKey(t testing.TB) key.Key {
	name := t.Name()
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
