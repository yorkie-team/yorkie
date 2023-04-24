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

// Package election provides leader election between server cluster. It is used to
// elect leader among server cluster and run tasks only on the leader.
package election

import (
	"context"

	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client is a client that connects to Kubernetes API server.
type Client struct {
	client  *clientset.Clientset
	podName string

	leaseLockNamespace string

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// NewClient creates a new instance of Client.
func NewClient(podName string, namespace string) (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	client, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Client{
		client:  client,
		podName: podName,

		leaseLockNamespace: namespace,

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}, nil
}
