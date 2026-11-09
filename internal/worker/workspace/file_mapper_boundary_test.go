package workspace

import "testing"

func TestIsSubpathUsesPathComponents(t *testing.T) {
	mapper := NewFileMapper("/workspace")
	for _, tc := range []struct {
		path, base string
		want       bool
	}{
		{"/workspace/task", "/workspace", true},
		{"/workspace", "/workspace", true},
		{"/workspace-other/task", "/workspace", false},
		{"/workspace/../other", "/workspace", false},
		{"/workspace/task", "/workspace/", true},
	} {
		if got := mapper.IsSubpath(tc.path, tc.base); got != tc.want {
			t.Errorf("IsSubpath(%q, %q) = %t, want %t", tc.path, tc.base, got, tc.want)
		}
	}
}
