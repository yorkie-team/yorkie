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

// EncodeToString encodes the given vector clock to string.
func (vc VectorClock) EncodeToString() (string, error) {
	bytes, err := json.Marshal(vc)
	if err != nil {
		return "", fmt.Errorf("marshal vector clock to bytes: %w", err)
	}

	return string(bytes), nil
}

// NewSyncedVectorMapFromJSON creates a new instance of SyncedVectorMap.
func NewSyncedVectorMapFromJSON(encodedChange string) (SyncedVectorMap, error) {
	if encodedChange == "" {
		return nil, nil
	}

	vc := SyncedVectorMap{}
	if err := json.Unmarshal([]byte(encodedChange), &vc); err != nil {
		return nil, fmt.Errorf("unmarshal vector clock: %w", err)
	}

	return vc, nil
}

// EncodeToString encodes the given syncedVectorMap to string.
func (svm SyncedVectorMap) EncodeToString() (string, error) {
	if svm == nil {
		return "", nil
	}

	bytes, err := json.Marshal(svm)
	if err != nil {
		return "", fmt.Errorf("marshal syncedVectorMap to bytes: %w", err)
	}

	return string(bytes), nil
}

// InitialSyncedVectorMap creates an initial synced vector map.
func InitialSyncedVectorMap(lamport int64) SyncedVectorMap {
	actorID := InitialActorID.String()
	return SyncedVectorMap{actorID: VectorClock{actorID: lamport}}
}

// MinSyncedVector returns the minimum vector clock from the given syncedVectorMap.
func (svm SyncedVectorMap) MinSyncedVector() VectorClock {
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
	return minSeqVector
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

// ChangeActorID change the InitialActorID into New ActorID.
// Move the initial actor's vector clock to the given actor's vector clock.
// And delete the initial actor's vector clock.
func (svm SyncedVectorMap) ChangeActorID(id *ActorID) {
	newID := id.String()
	// Already has actor id given from the server.
	if svm[newID] != nil {
		return
	}

	initID := InitialActorID.String()

	// If current actor id is initial actor id
	svm[newID] = svm[initID]
	svm[newID][newID] = svm[newID][initID]

	// delete initialID
	delete(svm[newID], initID)
	delete(svm, initID)
}
