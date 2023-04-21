// Package kubernetes provides the kubernetes implementation of the Election.
package kubernetes

import (
	"context"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// Client is a client that connects to K8s API server.
type Client struct {
	leaseLockName      string
	leaseLockNamespace string
	podName            string

	client *clientset.Clientset
}

// NewClient creates a new instance of Client.
func NewClient(leaseLockName string, leaseLockNamespace string) (*Client, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	client, err := clientset.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	return &Client{
		leaseLockName:      leaseLockName,
		leaseLockNamespace: leaseLockNamespace,
		podName:            os.Getenv("POD_NAME"),
		client:             client,
	}, nil
}

// StartElection starts the election.
func (c *Client) StartElection(onStartLeading func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      c.leaseLockName,
			Namespace: c.leaseLockNamespace,
		},
		Client: c.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: c.podName,
		},
	}

	// start the leader election code loop
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				//logging.From(ctx).Infof(
				//	"ELECTION: leader elected: %s",
				//	c.podName,
				//)
				onStartLeading(ctx)
			},
			OnStoppedLeading: func() {
				fmt.Println("[Leader Election]: leader lost: " + c.podName)
			},
			OnNewLeader: func(identity string) {
				fmt.Println("[Leader Election]: leader elected: " + identity)
				//logging.From(ctx).Infof(
				//	"ELECTION: new leader elected: %s",
				//	identity,
				//)
			},
		},
	})
}
