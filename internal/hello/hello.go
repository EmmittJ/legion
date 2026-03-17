package hello

import "fmt"

// Hello returns a greeting string for the given name.
func Hello(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}
