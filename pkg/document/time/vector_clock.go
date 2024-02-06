package time

import (
	"encoding/json"
	"fmt"
)

// VectorClock is a map of actor id to its sequence number.
type VectorClock map[string]int64

// SyncedVectorMap is a map of actor id to its vector clock.
type SyncedVectorMap map[string]VectorClock

// NewVectorClockFromJSON creates a new instance of VectorClock.
func NewVectorClockFromJSON(encodedChange string) (VectorClock, error) {
	if encodedChange == "" {
		return nil, nil
	}

	vc := VectorClock{}
	if err := json.Unmarshal([]byte(encodedChange), &vc); err != nil {
		return nil, fmt.Errorf("unmarshal vector clock: %w", err)
	}

	return vc, nil
}

// InitialSyncedVectorMap creates an initial synced vector map.
func InitialSyncedVectorMap(lamport int64) SyncedVectorMap {
	actorID := InitialActorID.String()
	return SyncedVectorMap{actorID: VectorClock{actorID: lamport}}
}

// MinSyncedVector returns the minimum vector clock from the given syncedVectorMap.
func (svm SyncedVectorMap) MinSyncedVector() *VectorClock {
	minSeqVector := make(VectorClock)
	checker := make(map[string]int64)
	for _, vec := range svm {
		for k, v := range vec {

			if checker[k] == 0 {
				checker[k] = v
				continue
			}

			if minSeqVector[k] == 0 {
				minSeqVector[k] = min(checker[k], v)
				continue
			}

			minSeqVector[k] = min(minSeqVector[k], v)

		}
	}

	return &minSeqVector
}

// Next update `vectorClock[id] += inc` and return the New VectorClock.
func (vc VectorClock) Next(id *ActorID, inc int64) VectorClock {
	if vc == nil {
		vc = make(VectorClock)
	}
	vc[id.String()] += inc
	return vc
}
