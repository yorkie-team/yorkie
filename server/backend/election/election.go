/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package election

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/server/logging"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// StartElection starts the election.
func (c *Client) StartElection(
	leaseLockName string,
	leaseDuration time.Duration,
	onStartLeading func(ctx context.Context),
	onStoppedLeading func(),
) {
	// create the resource lock for the leader election
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseLockName,
			Namespace: c.leaseLockNamespace,
		},
		Client: c.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: c.podName,
		},
	}

	// start the leader election code loop
	go leaderelection.RunOrDie(c.ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   leaseDuration,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				onStartLeading(ctx)
			},
			OnStoppedLeading: func() {
				logging.From(c.ctx).Infof(
					"ELECTION: leader stepped down: %s",
					c.podName,
				)
				onStoppedLeading()
			},
			OnNewLeader: func(identity string) {
				logging.From(c.ctx).Infof(
					"ELECTION: new leader elected: %s",
					identity,
				)
			},
		},
	})
}

// StopElection stops the election.
func (c *Client) StopElection() error {
	c.cancelFunc()
	return nil
}
