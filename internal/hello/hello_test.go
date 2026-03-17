package hello_test

import (
	"testing"

	"github.com/EmmittJ/legion/internal/hello"
)

func TestHello(t *testing.T) {
	got := hello.Hello("World")
	want := "Hello, World!"
	if got != want {
		t.Errorf("Hello(\"World\") = %q; want %q", got, want)
	}
}
