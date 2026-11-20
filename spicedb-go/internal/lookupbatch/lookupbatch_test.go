package lookupbatch

import "testing"

func TestDefault(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pageSize uint32
		limit    uint64
		single   bool
		want     uint32
	}{
		{"page size", 512, 0, false, 512},
		{"page size capped", 5000, 0, false, 1000},
		{"page size ignores limit", 512, 10, false, 512},
		{"single request without limit", 512, 0, true, 1000},
		{"single request limit", 512, 40, true, 40},
		{"single request limit capped", 512, 1 << 40, true, 1000},
	} {
		if got := Default(tc.pageSize, tc.limit, tc.single); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestForPage(t *testing.T) {
	for _, tc := range []struct {
		name            string
		batch, pageSize uint32
		limit, yielded  uint64
		single, filter  bool
		want            uint32
	}{
		{"no limit", 100, 512, 0, 0, false, false, 100},
		{"no limit, filtered", 100, 512, 0, 0, false, true, 100},
		{"disabled", 0, 512, 10, 0, false, false, 0},
		{"remaining below batch size", 100, 512, 30, 0, false, false, 30},
		{"remaining below batch size on a later page", 100, 512, 542, 512, false, false, 30},
		{"remaining a multiple of batch size", 100, 512, 300, 0, false, false, 100},
		{"remaining neither", 100, 512, 250, 0, false, false, 0},
		{"remaining at least a page", 100, 512, 600, 0, false, false, 100},
		{"remaining exactly a page", 100, 512, 512, 0, false, false, 100},
		{"limit reached", 100, 512, 10, 10, false, false, 100},
		{"limit with filter", 100, 512, 600, 0, false, true, 0},
		{"single request with limit", 100, 512, 250, 0, true, false, 100},
		{"single request with limit and filter", 100, 512, 250, 0, true, true, 100},
	} {
		if got := ForPage(tc.batch, tc.pageSize, tc.limit, tc.yielded, tc.single, tc.filter); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
