package util

import "strconv"

// FizzBuzz returns "Fizz" for multiples of 3, "Buzz" for multiples of 5,
// "FizzBuzz" for multiples of both, and the number as a string otherwise.
func FizzBuzz(n int) string {
	switch {
	case n%15 == 0:
		return "FizzBuzz"
	case n%3 == 0:
		return "Fizz"
	case n%5 == 0:
		return "Buzz"
	default:
		return strconv.Itoa(n)
	}
}
