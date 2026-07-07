package proxy

import "testing"

func TestNormalizeAPIURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"127.0.0.1:49544", "http://127.0.0.1:49544"},
		{"http://127.0.0.1:49544", "http://127.0.0.1:49544"},
		{"http:127.0.0.1:49544", "http://127.0.0.1:49544"},
		{"http:///127.0.0.1:49544", "http://127.0.0.1:49544"},
		{" https://127.0.0.1:49544 ", "https://127.0.0.1:49544"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeAPIURL(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeAPIURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
