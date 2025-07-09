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
	"github.com/yorkie-team/yorkie/server/backend/messagebroker"
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

	housekeepingInterval      time.Duration
	clientDeactivateThreshold string

	mongoConnectionURI                string
	mongoConnectionTimeout            time.Duration
	mongoYorkieDatabase               string
	mongoPingTimeout                  time.Duration
	mongoMonitoringEnabled            bool
	mongoMonitoringSlowQueryThreshold string

	pprofEnabled bool

	authWebhookMaxWaitInterval time.Duration
	authWebhookMinWaitInterval time.Duration
	authWebhookRequestTimeout  time.Duration
	authWebhookCacheTTL        time.Duration

	eventWebhookMaxWaitInterval time.Duration
	eventWebhookMinWaitInterval time.Duration
	eventWebhookRequestTimeout  time.Duration
	eventWebhookCacheTTL        time.Duration

	projectCacheTTL time.Duration

	kafkaAddresses    string
	kafkaTopic        string
	kafkaWriteTimeout time.Duration

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
			conf.Backend.ClientDeactivateThreshold = clientDeactivateThreshold

			conf.Backend.AuthWebhookMaxWaitInterval = authWebhookMaxWaitInterval.String()
			conf.Backend.AuthWebhookMinWaitInterval = authWebhookMinWaitInterval.String()
			conf.Backend.AuthWebhookRequestTimeout = authWebhookRequestTimeout.String()
			conf.Backend.AuthWebhookCacheTTL = authWebhookCacheTTL.String()

			conf.Backend.EventWebhookMaxWaitInterval = eventWebhookMaxWaitInterval.String()
			conf.Backend.EventWebhookMinWaitInterval = eventWebhookMinWaitInterval.String()
			conf.Backend.EventWebhookRequestTimeout = eventWebhookRequestTimeout.String()

			conf.Backend.ProjectCacheTTL = projectCacheTTL.String()

			if mongoConnectionURI != "" {
				conf.Mongo = &mongo.Config{
					ConnectionURI:                mongoConnectionURI,
					ConnectionTimeout:            mongoConnectionTimeout.String(),
					YorkieDatabase:               mongoYorkieDatabase,
					PingTimeout:                  mongoPingTimeout.String(),
					MonitoringEnabled:            mongoMonitoringEnabled,
					MonitoringSlowQueryThreshold: mongoMonitoringSlowQueryThreshold,
				}
			}

			if kafkaAddresses != "" && kafkaTopic != "" {
				conf.Kafka = &messagebroker.Config{
					Addresses:    kafkaAddresses,
					Topic:        kafkaTopic,
					WriteTimeout: kafkaWriteTimeout.String(),
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
		&conf.Housekeeping.CandidatesLimitPerProject,
		"housekeeping-candidates-limit-per-project",
		server.DefaultHousekeepingCandidatesLimitPerProject,
		"candidates limit per project for a single housekeeping run",
	)
	cmd.Flags().IntVar(
		&conf.Housekeeping.ProjectFetchSize,
		"housekeeping-project-fetch-size",
		server.DefaultHousekeepingProjectFetchSize,
		"housekeeping project fetch size for a single housekeeping run",
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
	cmd.Flags().StringVar(
		&clientDeactivateThreshold,
		"client-deactivate-threshold",
		server.DefaultClientDeactivateThreshold,
		"Deactivate threshold of clients in specific project for housekeeping.",
	)
	cmd.Flags().Int64Var(
		&conf.Backend.SnapshotThreshold,
		"backend-snapshot-threshold",
		server.DefaultSnapshotThreshold,
		"Threshold that determines if changes should be sent with snapshot when the number "+
			"of changes is greater than this value.",
	)
	cmd.Flags().Int64Var(
		&conf.Backend.SnapshotInterval,
		"backend-snapshot-interval",
		server.DefaultSnapshotInterval,
		"Interval of changes to create a snapshot.",
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
	cmd.Flags().DurationVar(
		&authWebhookRequestTimeout,
		"auth-webhook-request-timeout",
		server.DefaultAuthWebhookRequestTimeout,
		"Timeout for each authorization webhook request.",
	)
	cmd.Flags().Uint64Var(
		&conf.Backend.AuthWebhookMaxRetries,
		"auth-webhook-max-retries",
		server.DefaultAuthWebhookMaxRetries,
		"Maximum number of retries for authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&authWebhookMinWaitInterval,
		"auth-webhook-min-wait-interval",
		server.DefaultAuthWebhookMinWaitInterval,
		"Minimum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&authWebhookMaxWaitInterval,
		"auth-webhook-max-wait-interval",
		server.DefaultAuthWebhookMaxWaitInterval,
		"Maximum wait interval between retries(exponential backoff).",
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
	cmd.Flags().DurationVar(
		&eventWebhookRequestTimeout,
		"event-webhook-request-timeout",
		server.DefaultEventWebhookRequestTimeout,
		"Timeout for each event webhook request.",
	)
	cmd.Flags().Uint64Var(
		&conf.Backend.EventWebhookMaxRetries,
		"event-webhook-max-retries",
		server.DefaultEventWebhookMaxRetries,
		"Maximum number of retries for event webhook.",
	)
	cmd.Flags().DurationVar(
		&eventWebhookMinWaitInterval,
		"event-webhook-min-wait-interval",
		server.DefaultEventWebhookMinWaitInterval,
		"Minimum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().DurationVar(
		&eventWebhookMaxWaitInterval,
		"event-webhook-max-wait-interval",
		server.DefaultEventWebhookMaxWaitInterval,
		"Maximum wait interval between retries(exponential backoff).",
	)
	cmd.Flags().IntVar(
		&conf.Backend.ProjectCacheSize,
		"project-info-cache-size",
		server.DefaultProjectCacheSize,
		"The cache size of the project info.",
	)
	cmd.Flags().DurationVar(
		&projectCacheTTL,
		"project-info-cache-ttl",
		server.DefaultProjectCacheTTL,
		"TTL value to set when caching project info.",
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
		&kafkaAddresses,
		"kafka-addresses",
		"",
		"Comma-separated list of Kafka addresses (e.g., localhost:9092,localhost:9093)",
	)
	cmd.Flags().StringVar(
		&kafkaTopic,
		"kafka-topic",
		server.DefaultKafkaTopic,
		"Kafka topic name to publish events",
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

	rootCmd.AddCommand(cmd)
}
