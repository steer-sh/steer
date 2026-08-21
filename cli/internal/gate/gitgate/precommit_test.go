package gitgate

import (
	"testing"
)

// IndexSource is the only in-band signal a pre-hook gets about the form of the change.
func TestIndexSource(t *testing.T) {
	cases := []struct {
		env  string
		want string
		why  string
	}{
		{"", "default", "no variable means git handed the hook the repo's own index"},
		{".git/index", "index", "the pre-staged form, relative as git writes it"},
		{"/r/.git/index.lock", "index.lock", "the -a form reuses the lock as its temp index"},
		{"/r/.git/next-index-8626.lock", "next-index-8626.lock", "the pathspec form"},
	}
	for _, tc := range cases {
		got := IndexSource(func(string) string { return tc.env })
		if got != tc.want {
			t.Errorf("IndexSource(%q) = %q, want %q — %s", tc.env, got, tc.want, tc.why)
		}
	}
}
