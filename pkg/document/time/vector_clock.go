package time

import (
	"encoding/json"
	"fmt"
)

// VectorClock is a map of actor id to its sequence number.
type VectorClock map[string]int64

// NewVectorClockFromJSON creates a new instance of VectorClock.
func NewVectorClockFromJSON(encodedChange string) (VectorClock, error) {
	if encodedChange == "" {
		return VectorClock{}, nil
	}

	vc := VectorClock{}
	if err := json.Unmarshal([]byte(encodedChange), &vc); err != nil {
		return nil, fmt.Errorf("unmarshal vector clock: %w", err)
	}

	return vc, nil
}

// InitialVectorClock returns an initial vector clock.
func InitialVectorClock() VectorClock {
	return VectorClock{InitialActorID.String(): 0}
}

// ChangeActorID changes the initial ID to given ID of the vector clock.
func (vc VectorClock) ChangeActorID(id *ActorID) {
	newID := id.String()
	// Already has actor id given from the server.
	if vc[newID] != 0 {
		return
	}

	initID := InitialActorID.String()

	// If current actor id is initial actor id
	vc[newID] = vc[initID]

	// delete initialID
	delete(vc, initID)
}

// EncodeToString encodes the given vector clock to string.
func (vc VectorClock) EncodeToString() (string, error) {
	bytes, err := json.Marshal(vc)
	if err != nil {
		return "", fmt.Errorf("marshal vector clock to bytes: %w", err)
	}

	return string(bytes), nil
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
