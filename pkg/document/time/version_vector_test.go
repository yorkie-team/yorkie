package time_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestVersionVector(t *testing.T) {
	actor1, _ := time.ActorIDFromHex("000000000000000000000001")
	actor2, _ := time.ActorIDFromHex("000000000000000000000002")
	actor3, _ := time.ActorIDFromHex("000000000000000000000003")

	tests := []struct {
		name   string
		v1     time.VersionVector
		v2     time.VersionVector
		expect time.VersionVector
	}{
		{
			name:   "empty vectors",
			v1:     time.NewVersionVector(),
			v2:     time.NewVersionVector(),
			expect: time.NewVersionVector(),
		},
		{
			name: "v1 has values, v2 is empty",
			v1: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 5,
				actor2: 3,
			}),
			v2: time.NewVersionVector(),
			expect: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 0,
				actor2: 0,
			}),
		},
		{
			name: "v2 has values, v1 is empty",
			v1:   time.NewVersionVector(),
			v2: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 5,
				actor2: 3,
			}),
			expect: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 0,
				actor2: 0,
			}),
		},
		{
			name: "both vectors have same keys with different values",
			v1: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 5,
				actor2: 3,
			}),
			v2: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 3,
				actor2: 4,
			}),
			expect: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 3,
				actor2: 3,
			}),
		},
		{
			name: "vectors have different keys",
			v1: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 5,
				actor2: 3,
			}),
			v2: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor2: 4,
				actor3: 6,
			}),
			expect: helper.NewVersionVectorFromActors(map[*time.ActorID]int64{
				actor1: 0,
				actor2: 3,
				actor3: 0,
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.v1.Min(tc.v2)
			assert.Equal(t, tc.expect, result)
		})
	}
}
