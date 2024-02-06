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
	checker := make(map[string]int)
	for _, vec := range svm {
		for k, v := range vec {
			checker[k]++
			if minSeqVector[k] == 0 {
				minSeqVector[k] = v
			} else {
				minSeqVector[k] = min(minSeqVector[k], v)
			}
		}
	}

	for k, v := range checker {
		if v != len(svm) {
			minSeqVector[k] = 0
		}
	}
	return &minSeqVector
}

// Copy returns a new deep copied VectorClock.
func (vc VectorClock) Copy() VectorClock {
	if vc == nil {
		return nil
	}
	rep := make(VectorClock, len(vc))
	for k, v := range vc {
		rep[k] = v
	}
	return rep
}

// CopyAndSet copy vectorClock and update `replica[id] = value` and return the replica.
func (vc VectorClock) CopyAndSet(id string, value int64) VectorClock {
	if vc == nil {
		vc = make(VectorClock)
	}
	repl := vc.Copy()
	repl[id] = value
	return repl
}
