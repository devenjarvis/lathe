package extend_test

import (
	"testing"

	"github.com/devenjarvis/lathe/internal/extend"
)

func TestNextPartFilename(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{nil, "part-01.md"},
		{[]string{}, "part-01.md"},
		{[]string{"part-01.md"}, "part-02.md"},
		{[]string{"part-01.md", "part-02.md"}, "part-03.md"},
		{[]string{"part-01.md", "part-02.md", "part-03.md"}, "part-04.md"},
	}
	for _, tc := range cases {
		got := extend.NextPartFilename(tc.parts)
		if got != tc.want {
			t.Errorf("NextPartFilename(%v) = %q, want %q", tc.parts, got, tc.want)
		}
	}
}
