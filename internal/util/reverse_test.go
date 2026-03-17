package util_test

import (
	"testing"

	"github.com/EmmittJ/legion/internal/util"
)

func TestReverse(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "olleh"},
		{"", ""},
		{"a", "a"},
		{"abcd", "dcba"},
		{"Hello, 世界", "界世 ,olleH"},
	}
	for _, tt := range tests {
		got := util.Reverse(tt.input)
		if got != tt.want {
			t.Errorf("Reverse(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
