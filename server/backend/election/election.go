// Package election provides election functions for leader election within a cluster.
package election

import "context"

// Election provides election functions for leader election within a cluster.
type Election interface {
	// StartElection starts the election.
	StartElection(onStartLeading func(ctx context.Context))
}
