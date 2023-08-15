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
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	gracefulTimeout = 10 * time.Second
)

var (
	flagConfPath string
	flagLogLevel string

	adminTokenDuration        time.Duration
	housekeepingInterval      time.Duration
	clientDeactivateThreshold string

	mongoConnectionURI     string
	mongoConnectionTimeout time.Duration
	mongoYorkieDatabase    string
	mongoPingTimeout       time.Duration

	authWebhookMaxWaitInterval time.Duration
	authWebhookCacheAuthTTL    time.Duration
	authWebhookCacheUnauthTTL  time.Duration
	projectInfoCacheTTL        time.Duration

	conf = server.NewConfig()
)

func newServerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "server [options]",
		Short: "Start Yorkie server",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf.Backend.AdminTokenDuration = adminTokenDuration.String()

			conf.Backend.ClientDeactivateThreshold = clientDeactivateThreshold

			conf.Backend.AuthWebhookMaxWaitInterval = authWebhookMaxWaitInterval.String()
			conf.Backend.AuthWebhookCacheAuthTTL = authWebhookCacheAuthTTL.String()
			conf.Backend.AuthWebhookCacheUnauthTTL = authWebhookCacheUnauthTTL.String()
			conf.Backend.ProjectInfoCacheTTL = projectInfoCacheTTL.String()

			conf.Housekeeping.Interval = housekeepingInterval.String()

			if mongoConnectionURI != "" {
				conf.Mongo = &mongo.Config{
					ConnectionURI:     mongoConnectionURI,
					ConnectionTimeout: mongoConnectionTimeout.String(),
					YorkieDatabase:    mongoYorkieDatabase,
					PingTimeout:       mongoPingTimeout.String(),
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
	cmd.Flags().Uint64Var(
		&conf.RPC.MaxRequestBytes,
		"rpc-max-requests-bytes",
		server.DefaultRPCMaxRequestsBytes,
		"Maximum client request size in bytes the server will accept.",
	)
	cmd.Flags().StringVar(
		&conf.RPC.MaxConnectionAge,
		"rpc-max-connection-age",
		server.DefaultRPCMaxConnectionAge.String(),
		"Maximum duration of connection may exist before it will be closed by sending a GoAway.",
	)
	cmd.Flags().StringVar(
		&conf.RPC.MaxConnectionAgeGrace,
		"rpc-max-connection-age-grace",
		server.DefaultRPCMaxConnectionAgeGrace.String(),
		"Additional grace period after MaxConnectionAge after which connections will be forcibly closed.",
	)
	cmd.Flags().IntVar(
		&conf.Profiling.Port,
		"profiling-port",
		server.DefaultProfilingPort,
		"Profiling port",
	)
	cmd.Flags().BoolVar(
		&conf.Profiling.EnablePprof,
		"enable-pprof",
		false,
		"Enable runtime profiling data via HTTP server.",
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
	cmd.Flags().DurationVar(
		&adminTokenDuration,
		"backend-admin-token-duration",
		server.DefaultAdminTokenDuration,
		"The duration of the admin authentication token.",
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
		&conf.Backend.SnapshotWithPurgingChanges,
		"backend-snapshot-with-purging-changes",
		server.DefaultSnapshotWithPurgingChanges,
		"Whether to delete previous changes when the snapshot is created.",
	)
	cmd.Flags().Uint64Var(
		&conf.Backend.AuthWebhookMaxRetries,
		"auth-webhook-max-retries",
		server.DefaultAuthWebhookMaxRetries,
		"Maximum number of retries for an authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&authWebhookMaxWaitInterval,
		"auth-webhook-max-wait-interval",
		server.DefaultAuthWebhookMaxWaitInterval,
		"Maximum wait interval for authorization webhook.",
	)
	cmd.Flags().IntVar(
		&conf.Backend.AuthWebhookCacheSize,
		"auth-webhook-cache-size",
		server.DefaultAuthWebhookCacheSize,
		"The cache size of the authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&authWebhookCacheAuthTTL,
		"auth-webhook-cache-auth-ttl",
		server.DefaultAuthWebhookCacheAuthTTL,
		"TTL value to set when caching authorized webhook response.",
	)
	cmd.Flags().DurationVar(
		&authWebhookCacheUnauthTTL,
		"auth-webhook-cache-unauth-ttl",
		server.DefaultAuthWebhookCacheUnauthTTL,
		"TTL value to set when caching unauthorized webhook response.",
	)
	cmd.Flags().IntVar(
		&conf.Backend.ProjectInfoCacheSize,
		"project-info-cache-size",
		server.DefaultProjectInfoCacheSize,
		"The cache size of the project info.",
	)
	cmd.Flags().DurationVar(
		&projectInfoCacheTTL,
		"project-info-cache-ttl",
		server.DefaultProjectInfoCacheTTL,
		"TTL value to set when caching project info.",
	)
	cmd.Flags().StringVar(
		&conf.Backend.Hostname,
		"hostname",
		server.DefaultHostname,
		"Yorkie Server Hostname",
	)

	rootCmd.AddCommand(cmd)
}
