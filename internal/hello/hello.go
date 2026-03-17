package hello

import "fmt"

// HelloWorld prints a greeting and returns it.
func HelloWorld() string {
	msg := "Hello, World!"
	fmt.Println(msg)
	return msg
}
