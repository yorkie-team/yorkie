package time

import (
	"encoding/json"
	"fmt"
)

type VectorClock map[string]int64

func NewVectorClockFromJson(encodedChange string) (VectorClock, error) {
	if encodedChange == "" {
		return nil, nil
	}

	vc := VectorClock{}
	if err := json.Unmarshal([]byte(encodedChange), &vc); err != nil {
		return nil, fmt.Errorf("unmarshal vector clock: %w", err)
	}

	return vc, nil
}
