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

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
	"github.com/yorkie-team/yorkie/server/backend/warehouse"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	gracefulTimeout = 10 * time.Second
)

var (
	flagConfPath string
	flagLogLevel string

	adminTokenDuration            time.Duration
	authGitHubClientID            string
	authGitHubClientSecret        string
	authGitHubRedirectURL         string
	authGitHubCallbackRedirectURL string
	authGitHubUserURL             string
	authGitHubAuthURL             string
	authGitHubTokenURL            string
	authGitHubDeviceAuthURL       string

	channelSessionTTL             time.Duration
	channelSessionCleanupInterval time.Duration
	channelSessionCountCacheTTL   time.Duration
	channelSessionCountCacheSize  int

	housekeepingInterval time.Duration

	mongoConnectionURI                string
	mongoConnectionTimeout            time.Duration
	mongoYorkieDatabase               string
	mongoPingTimeout                  time.Duration
	mongoMonitoringEnabled            bool
	mongoMonitoringSlowQueryThreshold string
	mongoCacheStatsEnabled            bool
	mongoCacheStatsInterval           time.Duration
	mongoProjectCacheSize             int
	mongoProjectCacheTTL              time.Duration
	mongoClientCacheSize              int
	mongoDocCacheSize                 int
	mongoChangeCacheSize              int
	mongoVectorCacheSize              int

	pprofEnabled bool

	authWebhookCacheTTL time.Duration

	kafkaAddresses           string
	kafkaUserEventsTopic     string
	kafkaDocumentEventsTopic string
	kafkaClientEventsTopic   string
	kafkaChannelEventsTopic  string
	kafkaSessionEventsTopic  string
	kafkaWriteTimeout        time.Duration

	starRocksDSN string

	conf = server.NewConfig()
)

func newServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server [options]",
		Short: "Start Yorkie server",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf.Backend.AdminTokenDuration = adminTokenDuration.String()
			conf.RPC.Auth.GitHubClientID = authGitHubClientID
			conf.RPC.Auth.GitHubClientSecret = authGitHubClientSecret
			conf.RPC.Auth.GitHubRedirectURL = authGitHubRedirectURL
			conf.RPC.Auth.GitHubCallbackRedirectURL = authGitHubCallbackRedirectURL
			conf.RPC.Auth.GitHubUserURL = authGitHubUserURL
			conf.RPC.Auth.GitHubAuthURL = authGitHubAuthURL
			conf.RPC.Auth.GitHubTokenURL = authGitHubTokenURL
			conf.RPC.Auth.GitHubDeviceAuthURL = authGitHubDeviceAuthURL

			conf.Profiling.PprofEnabled = pprofEnabled

			conf.Housekeeping.Interval = housekeepingInterval.String()

			conf.Backend.AuthWebhookCacheTTL = authWebhookCacheTTL.String()
			conf.Backend.ChannelSessionTTL = channelSessionTTL.String()
			conf.Backend.ChannelSessionCleanupInterval = channelSessionCleanupInterval.String()
			conf.Backend.ChannelSessionCountCacheTTL = channelSessionCountCacheTTL.String()
			conf.Backend.ChannelSessionCountCacheSize = channelSessionCountCacheSize

			if mongoConnectionURI != "" {
				conf.Mongo = &mongo.Config{
					ConnectionURI:                mongoConnectionURI,
					ConnectionTimeout:            mongoConnectionTimeout.String(),
					YorkieDatabase:               mongoYorkieDatabase,
					PingTimeout:                  mongoPingTimeout.String(),
					MonitoringEnabled:            mongoMonitoringEnabled,
					MonitoringSlowQueryThreshold: mongoMonitoringSlowQueryThreshold,
					CacheStatsEnabled:            mongoCacheStatsEnabled,
					CacheStatsInterval:           mongoCacheStatsInterval.String(),
					ProjectCacheSize:             mongoProjectCacheSize,
					ProjectCacheTTL:              mongoProjectCacheTTL.String(),
					ClientCacheSize:              mongoClientCacheSize,
					DocCacheSize:                 mongoDocCacheSize,
					ChangeCacheSize:              mongoChangeCacheSize,
					VectorCacheSize:              mongoVectorCacheSize,
				}
			}

			if kafkaAddresses != "" {
				conf.Kafka = &messaging.Config{
					Addresses:           kafkaAddresses,
					UserEventsTopic:     kafkaUserEventsTopic,
					DocumentEventsTopic: kafkaDocumentEventsTopic,
					ClientEventsTopic:   kafkaClientEventsTopic,
					ChannelEventsTopic:  kafkaChannelEventsTopic,
					SessionEventsTopic:  kafkaSessionEventsTopic,
					WriteTimeout:        kafkaWriteTimeout.String(),
				}
			}

			if starRocksDSN != "" {
				conf.StarRocks = &warehouse.Config{
					DSN: starRocksDSN,
				}
			}

			// If config file is given, command-line arguments will be overwritten.
			if flagConfPath != "" {
				parsed, err := server.NewConfigFromFile(flagConfPath)
				if err != nil {
					return err
				}
				conf = parsed
			}

			if err := logging.SetLogLevel(flagLogLevel); err != nil {
				return err
			}

			y, err := server.New(conf)
			if err != nil {
				return err
			}

			if err := y.Start(); err != nil {
				return err
			}

			if code := handleSignal(y); code != 0 {
				return fmt.Errorf("exit code: %d", code)
			}

			return nil
		},
	}
}

func handleSignal(r *server.Yorkie) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var sig os.Signal
	select {
	case s := <-sigCh:
		sig = s
	case <-r.ShutdownCh():
		// yorkie is already shutdown
		return 0
	}

	graceful := false
	if sig == syscall.SIGINT || sig == syscall.SIGTERM {
		graceful = true
	}

	gracefulCh := make(chan struct{})
	go func() {
		if err := r.Shutdown(graceful); err != nil {
			return
		}
		close(gracefulCh)
	}()

	select {
	case <-sigCh:
		return 1
	case <-time.After(gracefulTimeout):
		return 1
	case <-gracefulCh:
		return 0
	}
}

func init() {
	cmd := newServerCmd()
	cmd.Flags().StringVarP(
		&flagConfPath,
		"config",
		"c",
		"",
		"Config path",
	)
	cmd.Flags().StringVarP(
		&flagLogLevel,
		"log-level",
		"l",
		"info",
		"Log level: debug, info, warn, error, panic, fatal",
	)
	cmd.Flags().IntVar(
		&conf.RPC.Port,
		"rpc-port",
		server.DefaultRPCPort,
		"RPC port",
	)
	cmd.Flags().StringVar(
		&conf.RPC.CertFile,
		"rpc-cert-file",
		"",
		"RPC certification file's path",
	)
	cmd.Flags().StringVar(
		&conf.RPC.KeyFile,
		"rpc-key-file",
		"",
		"RPC key file's path",
	)
	cmd.Flags().IntVar(
		&conf.Profiling.Port,
		"profiling-port",
		server.DefaultProfilingPort,
		"Profiling port",
	)
	cmd.Flags().BoolVar(
		&pprofEnabled,
		"pprof-enabled",
		false,
		"Enable runtime profiling data via HTTP server.",
	)

	cmd.Flags().DurationVar(
		&adminTokenDuration,
		"backend-admin-token-duration",
		server.DefaultAdminTokenDuration,
		"The duration of the admin authentication token.",
	)
	cmd.Flags().StringVar(
		&authGitHubClientID,
		"auth-github-client-id",
		"",
		"GitHub OAuth Client ID",
	)
	cmd.Flags().StringVar(
		&authGitHubClientSecret,
		"auth-github-client-secret",
		"",
		"GitHub OAuth Client Secret",
	)
	cmd.Flags().StringVar(
		&authGitHubRedirectURL,
		"auth-github-redirect-url",
		"http://localhost:8080/auth/github/callback",
		"GitHub OAuth callback URL",
	)
	cmd.Flags().StringVar(
		&authGitHubCallbackRedirectURL,
		"auth-github-callback-redirect-url",
		"http://localhost:5173/dashboard",
		"GitHub OAuth callback redirect URL",
	)
	cmd.Flags().StringVar(
		&authGitHubUserURL,
		"auth-github-user-url",
		server.DefaultGitHubUserURL,
		"GitHub User API URL",
	)
	cmd.Flags().StringVar(
		&authGitHubAuthURL,
		"auth-github-auth-url",
		server.DefaultGitHubAuthURL,
		"GitHub OAuth2 authorization URL",
	)
	cmd.Flags().StringVar(
		&authGitHubTokenURL,
		"auth-github-token-url",
		server.DefaultGitHubTokenURL,
		"GitHub OAuth2 token URL",
	)
	cmd.Flags().StringVar(
		&authGitHubDeviceAuthURL,
		"auth-github-device-auth-url",
		server.DefaultGitHubDeviceAuthURL,
		"GitHub OAuth2 device authorization URL",
	)

	cmd.Flags().DurationVar(
		&housekeepingInterval,
		"housekeeping-interval",
		server.DefaultHousekeepingInterval,
		"housekeeping interval between housekeeping runs",
	)
	cmd.Flags().IntVar(
		&conf.Housekeeping.CandidatesLimit,
		"housekeeping-candidates-limit",
		server.DefaultHousekeepingCandidatesLimit,
		"candidates limit for a single housekeeping run",
	)
	cmd.Flags().IntVar(
		&conf.Housekeeping.CompactionMinChanges,
		"housekeeping-compaction-min-changes",
		server.DefaultHousekeepingCompactionMinChanges,
		"minimum number of changes to compact a document for housekeeping run",
	)
	cmd.Flags().StringVar(
		&mongoConnectionURI,
		"mongo-connection-uri",
		"",
		"MongoDB's connection URI",
	)
	cmd.Flags().DurationVar(
		&mongoConnectionTimeout,
		"mongo-connection-timeout",
		server.DefaultMongoConnectionTimeout,
		"Mongo DB's connection timeout",
	)
	cmd.Flags().StringVar(
		&mongoYorkieDatabase,
		"mongo-yorkie-database",
		server.DefaultMongoYorkieDatabase,
		"Yorkie's database name in MongoDB",
	)
	cmd.Flags().DurationVar(
		&mongoPingTimeout,
		"mongo-ping-timeout",
		server.DefaultMongoPingTimeout,
		"Mongo DB's ping timeout",
	)
	cmd.Flags().BoolVar(
		&mongoMonitoringEnabled,
		"mongo-monitoring-enabled",
		false,
		"Enable MongoDB query monitoring",
	)
	cmd.Flags().StringVar(
		&mongoMonitoringSlowQueryThreshold,
		"mongo-monitoring-slow-query-threshold",
		"100ms",
		"Threshold for logging slow MongoDB queries (e.g. '100ms', '1s')",
	)
	cmd.Flags().BoolVar(
		&mongoCacheStatsEnabled,
		"mongo-cache-stats-enabled",
		false,
		"Enable MongoDB cache statistics logging",
	)
	cmd.Flags().IntVar(
		&mongoProjectCacheSize,
		"mongo-project-cache-size",
		server.DefaultProjectCacheSize,
		"MongoDB project cache size",
	)
	cmd.Flags().DurationVar(
		&mongoProjectCacheTTL,
		"mongo-project-cache-ttl",
		5*time.Minute,
		"TTL for MongoDB project cache (e.g. '5m', '60s')",
	)
	cmd.Flags().IntVar(
		&mongoClientCacheSize,
		"mongo-client-cache-size",
		server.DefaultMongoClientCacheSize,
		"MongoDB client cache size",
	)
	cmd.Flags().IntVar(
		&mongoDocCacheSize,
		"mongo-doc-cache-size",
		server.DefaultMongoDocCacheSize,
		"MongoDB document cache size",
	)
	cmd.Flags().IntVar(
		&mongoChangeCacheSize,
		"mongo-change-cache-size",
		server.DefaultMongoChangeCacheSize,
		"MongoDB change cache size",
	)
	cmd.Flags().IntVar(
		&mongoVectorCacheSize,
		"mongo-vector-cache-size",
		server.DefaultMongoVectorCacheSize,
		"MongoDB version vector cache size",
	)
	cmd.Flags().DurationVar(
		&mongoCacheStatsInterval,
		"mongo-cache-stats-interval",
		30*time.Second,
		"Interval for logging MongoDB cache statistics (e.g. '30s', '1m')",
	)
	cmd.Flags().StringVar(
		&conf.Backend.AdminUser,
		"backend-admin-user",
		server.DefaultAdminUser,
		"The name of the default admin user, who has full permissions.",
	)
	cmd.Flags().StringVar(
		&conf.Backend.AdminPassword,
		"backend-admin-password",
		server.DefaultAdminPassword,
		"The password of the default admin.",
	)
	cmd.Flags().StringVar(
		&conf.Backend.SecretKey,
		"backend-secret-key",
		server.DefaultSecretKey,
		"The secret key for signing authentication tokens for admin users.",
	)

	cmd.Flags().BoolVar(
		&conf.Backend.UseDefaultProject,
		"backend-use-default-project",
		server.DefaultUseDefaultProject,
		"Whether to use the default project. Even if public key is not provided from the client, "+
			"the default project will be used for the request.",
	)
	cmd.Flags().BoolVar(
		&conf.Backend.SnapshotDisableGC,
		"backend-snapshot-disable-gc",
		server.DefaultSnapshotDisableGC,
		"Whether to disable garbage collection of snapshots.",
	)
	cmd.Flags().IntVar(
		&conf.Backend.SnapshotCacheSize,
		"snapshot-cache-size",
		server.DefaultSnapshotCacheSize,
		"The cache size of the snapshots.",
	)
	cmd.Flags().BoolVar(
		&conf.Backend.DisableWebhookValidation,
		"backend-disable-webhook-validation",
		false,
		"Whether to disable webhook validation. This is useful for testing purposes.",
	)
	cmd.Flags().IntVar(
		&conf.Backend.AuthWebhookCacheSize,
		"auth-webhook-cache-size",
		server.DefaultAuthWebhookCacheSize,
		"The cache size of the authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&authWebhookCacheTTL,
		"auth-webhook-cache-auth-ttl",
		server.DefaultAuthWebhookCacheTTL,
		"TTL value to set when caching authorization webhook response.",
	)
	cmd.Flags().StringVar(
		&conf.Backend.Hostname,
		"hostname",
		server.DefaultHostname,
		"Yorkie Server Hostname",
	)
	cmd.Flags().StringVar(
		&conf.Backend.GatewayAddr,
		"backend-gateway-addr",
		server.DefaultGatewayAddr,
		"Gateway address",
	)
	cmd.Flags().StringVar(
		&conf.Backend.RPCAddr,
		"backend-rpc-addr",
		server.DefaultBackendRPCAddr,
		"Backend RPC address",
	)
	cmd.Flags().StringVar(
		&kafkaAddresses,
		"kafka-addresses",
		"",
		"Comma-separated list of Kafka addresses (e.g., localhost:9092,localhost:9093)",
	)
	cmd.Flags().StringVar(
		&kafkaUserEventsTopic,
		"kafka-user-events-topic",
		server.DefaultKafkaUserEventsTopic,
		"Kafka topic name to publish user events",
	)
	cmd.Flags().StringVar(
		&kafkaDocumentEventsTopic,
		"kafka-document-events-topic",
		server.DefaultKafkaDocumentEventsTopic,
		"Kafka topic name to publish document events",
	)
	cmd.Flags().StringVar(
		&kafkaClientEventsTopic,
		"kafka-client-events-topic",
		server.DefaultKafkaClientEventsTopic,
		"Kafka topic name to publish client events",
	)
	cmd.Flags().StringVar(
		&kafkaChannelEventsTopic,
		"kafka-channel-events-topic",
		server.DefaultKafkaChannelEventsTopic,
		"Kafka topic name to publish channel events",
	)
	cmd.Flags().StringVar(
		&kafkaSessionEventsTopic,
		"kafka-session-events-topic",
		server.DefaultKafkaSessionEventsTopic,
		"Kafka topic name to publish session events",
	)
	cmd.Flags().DurationVar(
		&kafkaWriteTimeout,
		"kafka-write-timeout",
		server.DefaultKafkaWriteTimeout,
		"Timeout for writing messages to Kafka",
	)
	cmd.Flags().StringVar(
		&starRocksDSN,
		"starrocks-dsn",
		"",
		"StarRocks DSN for the analytics",
	)
	cmd.Flags().DurationVar(
		&channelSessionTTL,
		"channel-session-ttl",
		server.DefaultChannelSessionTTL,
		"The TTL value for channel sessions.",
	)
	cmd.Flags().DurationVar(
		&channelSessionCleanupInterval,
		"channel-session-cleanup-interval",
		server.DefaultChannelSessionCleanupInterval,
		"The interval for running cleanup of expired channel sessions.",
	)
	cmd.Flags().DurationVar(
		&channelSessionCountCacheTTL,
		"channel-session-count-cache-ttl",
		server.DefaultChannelSessionCountCacheTTL,
		"The TTL value for channel session count cache.",
	)
	cmd.Flags().IntVar(
		&channelSessionCountCacheSize,
		"channel-session-count-cache-size",
		server.DefaultChannelSessionCountCacheSize,
		"The cache size of the channel session count.",
	)
	rootCmd.AddCommand(cmd)
}
