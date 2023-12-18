/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

	"github.com/rs/xid"
	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/backend/housekeeping"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	memsync "github.com/yorkie-team/yorkie/server/backend/sync/memory"
	"github.com/yorkie-team/yorkie/server/logging"
)

func newHousekeepingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "housekeeping [options]",
		Short: "Start Yorkie housekeeping routine",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf.Backend.ClientDeactivateThreshold = clientDeactivateThreshold
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

			hostname := conf.Backend.Hostname
			if hostname == "" {
				hostname, err := os.Hostname()
				if err != nil {
					return fmt.Errorf("os.Hostname: %w", err)
				}
				conf.Backend.Hostname = hostname
			}

			serverInfo := &sync.ServerInfo{
				ID:        xid.New().String(),
				Hostname:  hostname,
				UpdatedAt: time.Now(),
			}
			dbInfo := conf.Mongo.ConnectionURI

			db, err := mongo.Dial(conf.Mongo)
			if err != nil {
				return err
			}

			h, err := housekeeping.Start(
				conf.Housekeeping,
				db,
				memsync.NewCoordinator(serverInfo),
			)
			if err != nil {
				return err
			}

			logging.DefaultLogger().Infof(
				"Housekeeping started: ID: %s, RPC: %s",
				serverInfo.ID,
				dbInfo,
			)

			if err := h.Start(); err != nil {
				return err
			}

			if code := handleHousekeepingSignal(h); code != 0 {
				return fmt.Errorf("exit code: %d", code)
			}

			return nil
		},
	}
}

func handleHousekeepingSignal(h *housekeeping.Housekeeping) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	var _ os.Signal
	select {
	case s := <-sigCh:
		_ = s
	}

	gracefulCh := make(chan struct{})
	go func() {
		if err := h.Stop(); err != nil {
			return
		}
		close(gracefulCh)
	}()

	select {
	case <-sigCh:
		return 1
	case <-gracefulCh:
		return 0
	}
}

func init() {
	cmd := newHousekeepingCmd()
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
		&clientDeactivateThreshold,
		"client-deactivate-threshold",
		server.DefaultClientDeactivateThreshold,
		"Deactivate threshold of clients in specific project for housekeeping.",
	)
	cmd.Flags().StringVar(
		&conf.Backend.Hostname,
		"hostname",
		server.DefaultHostname,
		"Yorkie Server Hostname",
	)

	rootCmd.AddCommand(cmd)
}
