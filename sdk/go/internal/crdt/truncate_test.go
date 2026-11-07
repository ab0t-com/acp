package crdt

import "testing"

func TestIsTruncation(t *testing.T) {
	for _, tc := range []struct {
		before, after int
		want          bool
	}{
		{255, 0, false},  // below the floor: never guarded
		{256, 0, true},   // emptied
		{256, 25, true},  // 25*10 < 256
		{256, 26, false}, // keeps more than 10 %
		{1000, 99, true},
		{1000, 100, false},
		{1000, 1000, false},
		{0, 0, false},
	} {
		if got := IsTruncation(tc.before, tc.after); got != tc.want {
			t.Errorf("IsTruncation(%d, %d) = %v, want %v", tc.before, tc.after, got, tc.want)
		}
	}
}
