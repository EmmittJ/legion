package util

import "testing"

func TestCaesar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		shift int
		want  string
	}{
		{"basic shift 3", "ABC", 3, "DEF"},
		{"lowercase", "xyz", 3, "abc"},
		{"wrap around", "xyz", 3, "abc"},
		{"non-letters preserved", "Hello, World!", 13, "Uryyb, Jbeyq!"},
		{"zero shift", "Hello", 0, "Hello"},
		{"negative shift", "DEF", -3, "ABC"},
		{"shift 26 identity", "Hello", 26, "Hello"},
		{"decrypt", "Uryyb", -13, "Hello"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Caesar(tc.input, tc.shift)
			if got != tc.want {
				t.Errorf("Caesar(%q, %d) = %q, want %q", tc.input, tc.shift, got, tc.want)
			}
		})
	}
}
