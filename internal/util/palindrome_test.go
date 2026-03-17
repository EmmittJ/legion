package util

import "testing"

func TestIsPalindrome(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"racecar", true},
		{"A man a plan a canal Panama", true},
		{"Was it a car or a cat I saw", true},
		{"hello", false},
		{"", true},
		{"a", true},
		{"12321", true},
		{"12345", false},
	}
	for _, tc := range cases {
		got := IsPalindrome(tc.input)
		if got != tc.want {
			t.Errorf("IsPalindrome(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
