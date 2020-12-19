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

	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie"
)

var (
	gracefulTimeout = 10 * time.Second
)

var (
	flagConfPath string
)

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent [options]",
		Short: "Starts yorkie agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			conf := yorkie.NewConfig()
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
		"config path",
	)
	rootCmd.AddCommand(cmd)
}
