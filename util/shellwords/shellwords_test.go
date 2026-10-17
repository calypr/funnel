package shellwords

import (
	"reflect"
	"testing"
)

func TestSplitPreservesExpansionsAsLiteralText(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{`sh -c "echo $MSG"`, []string{"sh", "-c", "echo $MSG"}},
		{`echo $MSG`, []string{"echo", "$MSG"}},
		{`echo "$MSG extra"`, []string{"echo", "$MSG extra"}},
		{`echo ${MSG:-fallback}`, []string{"echo", "${MSG:-fallback}"}},
		{`echo $((1+2))`, []string{"echo", "$((1+2))"}},
		{`'a b' c\ d`, []string{"a b", "c d"}},
	} {
		got, err := Split(tt.in)
		if err != nil {
			t.Fatalf("Split(%q): %v", tt.in, err)
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Split(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitRejectsCommandSubstitution(t *testing.T) {
	if _, err := Split(`echo $(id)`); err == nil {
		t.Fatal("Split accepted command substitution, want error")
	}
	if _, err := Split(`echo <(id)`); err == nil {
		t.Fatal("Split accepted process substitution, want error")
	}
}
