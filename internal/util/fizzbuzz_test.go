package util_test

import (
	"testing"

	"github.com/EmmittJ/legion/internal/util"
)

func TestFizzBuzz(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{1, "1"},
		{2, "2"},
		{3, "Fizz"},
		{4, "4"},
		{5, "Buzz"},
		{6, "Fizz"},
		{10, "Buzz"},
		{15, "FizzBuzz"},
		{30, "FizzBuzz"},
		{7, "7"},
	}

	for _, tt := range tests {
		got := util.FizzBuzz(tt.input)
		if got != tt.expected {
			t.Errorf("FizzBuzz(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
