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

package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/yorkie"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync/etcd"
)

var (
	gracefulTimeout = 10 * time.Second
)

var (
	flagConfPath string

	mongoConnectionTimeout time.Duration
	mongoPingTimeout       time.Duration

	authWebhookMaxWaitInterval time.Duration
	authWebhookCacheAuthTTL    time.Duration
	authWebhookCacheUnauthTTL  time.Duration

	etcdEndpoints     []string
	etcdDialTimeout   time.Duration
	etcdUsername      string
	etcdPassword      string
	etcdLockLeaseTime time.Duration

	conf = yorkie.NewConfig()
)

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent [options]",
		Short: "Starts yorkie agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf.Mongo.ConnectionTimeout = mongoConnectionTimeout.String()
			conf.Mongo.PingTimeout = mongoPingTimeout.String()
			conf.Backend.AuthWebhookMaxWaitInterval = authWebhookMaxWaitInterval.String()
			conf.Backend.AuthWebhookCacheAuthTTL = authWebhookCacheAuthTTL.String()
			conf.Backend.AuthWebhookCacheUnauthTTL = authWebhookCacheUnauthTTL.String()

			if etcdEndpoints != nil {
				conf.ETCD = &etcd.Config{
					Endpoints:     etcdEndpoints,
					DialTimeout:   etcdDialTimeout.String(),
					Username:      etcdUsername,
					Password:      etcdPassword,
					LockLeaseTime: etcdLockLeaseTime.String(),
				}
			}

			// If config file is given, command-line arguments will be overwritten.
			if flagConfPath != "" {
				parsed, err := yorkie.NewConfigFromFile(flagConfPath)
				if err != nil {
					return fmt.Errorf(
						"fail to create config: %s",
						flagConfPath,
					)
				}
				conf = parsed
			}

			r, err := yorkie.New(conf)
			if err != nil {
				return err
			}

			if err := r.Start(); err != nil {
				return err
			}

			if code := handleSignal(r); code != 0 {
				return fmt.Errorf("exit code: %d", code)
			}

			return nil
		},
	}
}

func handleSignal(r *yorkie.Yorkie) int {
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

	log.Logger.Infof("Caught signal: %s", sig.String())

	gracefulCh := make(chan struct{})
	go func() {
		if err := r.Shutdown(graceful); err != nil {
			log.Logger.Error(err)
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
	cmd := newAgentCmd()
	cmd.Flags().StringVarP(
		&flagConfPath,
		"config",
		"c",
		"",
		"Config path",
	)
	cmd.Flags().IntVar(
		&conf.RPC.Port,
		"rpc-port",
		yorkie.DefaultRPCPort,
		"RPC port",
	)
	cmd.Flags().StringVar(
		&conf.RPC.CertFile,
		"rpc-cert-file",
		"",
		"RPC certification file's path",
	)
	cmd.Flags().StringVar(
		&conf.RPC.CertFile,
		"rpc-key-file",
		"",
		"RPC key file's path",
	)
	cmd.Flags().IntVar(
		&conf.Metrics.Port,
		"metrics-port",
		yorkie.DefaultMetricsPort,
		"Metrics port",
	)
	cmd.Flags().DurationVar(
		&mongoConnectionTimeout,
		"mongo-connection-timeout",
		yorkie.DefaultMongoConnectionTimeout,
		"Mongo DB's connection timeout",
	)
	cmd.Flags().StringVar(
		&conf.Mongo.ConnectionURI,
		"mongo-connection-uri",
		yorkie.DefaultMongoConnectionURI,
		"MongoDB's connection URI",
	)
	cmd.Flags().StringVar(
		&conf.Mongo.YorkieDatabase,
		"mongo-yorkie-database",
		yorkie.DefaultMongoYorkieDatabase,
		"Yorkie's database name in MongoDB",
	)
	cmd.Flags().DurationVar(
		&mongoPingTimeout,
		"mongo-ping-timeout",
		yorkie.DefaultMongoPingTimeout,
		"Mongo DB's ping timeout",
	)
	cmd.Flags().StringSliceVar(
		&etcdEndpoints,
		"etcd-endpoints",
		nil,
		"Comma separated list of etcd endpoints",
	)
	cmd.Flags().DurationVar(
		&etcdDialTimeout,
		"etcd-dial-timeout",
		etcd.DefaultDialTimeout,
		"ETCD's dial timeout",
	)
	cmd.Flags().StringVar(
		&etcdUsername,
		"etcd-username",
		"",
		"ETCD's user name",
	)
	cmd.Flags().StringVar(
		&etcdPassword,
		"etcd-password",
		"",
		"ETCD's password",
	)
	cmd.Flags().DurationVar(
		&etcdLockLeaseTime,
		"etcd-lock-lease-time",
		etcd.DefaultLockLeaseTime,
		"ETCD's lease time for lock",
	)
	cmd.Flags().Uint64Var(
		&conf.Backend.SnapshotThreshold,
		"backend-snapshot-threshold",
		yorkie.DefaultSnapshotThreshold,
		"Threshold that determines if changes should be sent with snapshot when the number "+
			"of changes is greater than this value.",
	)
	cmd.Flags().Uint64Var(
		&conf.Backend.SnapshotInterval,
		"backend-snapshot-interval",
		yorkie.DefaultSnapshotInterval,
		"Interval of changes to create a snapshot",
	)
	cmd.Flags().StringVar(
		&conf.Backend.AuthWebhookURL,
		"auth-webhook-url",
		"",
		"URL of remote service to query authorization",
	)
	cmd.Flags().StringSliceVar(
		&conf.Backend.AuthWebhookMethods,
		"auth-webhook-methods",
		[]string{},
		"List of methods that require authorization checks."+
			" If no value is specified, all methods will be checked.",
	)
	cmd.Flags().Uint64Var(
		&conf.Backend.AuthWebhookMaxRetries,
		"authorization-webhook-max-retries",
		yorkie.DefaultAuthWebhookMaxRetries,
		"Maximum number of retries for an authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&authWebhookMaxWaitInterval,
		"auth-webhook-max-wait-interval",
		yorkie.DefaultAuthWebhookMaxWaitInterval,
		"Maximum wait interval for authorization webhook.",
	)
	cmd.Flags().DurationVar(
		&authWebhookCacheAuthTTL,
		"auth-webhook-cache-auth-ttl",
		yorkie.DefaultAuthWebhookCacheAuthTTL,
		"TTL value to set when caching authorized webhook response.",
	)
	cmd.Flags().DurationVar(
		&authWebhookCacheUnauthTTL,
		"auth-webhook-cache-unauth-ttl",
		yorkie.DefaultAuthWebhookCacheUnauthTTL,
		"TTL value to set when caching unauthorized webhook response.",
	)

	rootCmd.AddCommand(cmd)
}
