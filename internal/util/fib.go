package util

// Fib returns the nth Fibonacci number (0-indexed: Fib(0)=0, Fib(1)=1).
// It panics for negative n.
func Fib(n int) int {
	if n < 0 {
		panic("Fib: negative argument")
	}
	a, b := 0, 1
	for i := 0; i < n; i++ {
		a, b = b, a+b
	}
	return a
}
