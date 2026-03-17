package util

// Caesar shifts each ASCII letter in s by shift positions, wrapping within
// the alphabet. Non-letter characters are left unchanged. shift may be
// negative for a reverse (decryption) shift.
func Caesar(s string, shift int) string {
	shift = ((shift % 26) + 26) % 26 // normalise to [0, 25]
	result := make([]byte, len(s))
	for i := range s {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			result[i] = 'a' + (c-'a'+byte(shift))%26
		case c >= 'A' && c <= 'Z':
			result[i] = 'A' + (c-'A'+byte(shift))%26
		default:
			result[i] = c
		}
	}
	return string(result)
}
