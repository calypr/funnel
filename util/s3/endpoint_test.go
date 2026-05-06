package s3util

import "testing"

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://example.com", "example.com"},
		{"https://example.com", "example.com"},
		{"http://example.com/path", "example.com/path"},
		{"https://example.com/path", "example.com/path"},
		{"example.com", "example.com"},
		{"example.com/path", "example.com/path"},
		{"http://example.com/", "example.com"},
		{"https://example.com/", "example.com"},
		{"http://example.com/path/", "example.com/path"},
		{"https://example.com/path/", "example.com/path"},
		{"", ""},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			result := ParseEndpoint(test.input)
			if result != test.expected {
				t.Errorf("parseEndpoint(%q) = %q; want %q", test.input, result, test.expected)
			}
		})
	}
}
