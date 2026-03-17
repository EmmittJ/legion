package util

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestHelloWorld(t *testing.T) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w

	HelloWorld()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)

	got := buf.String()
	want := "Hello, World!\n"
	if got != want {
		t.Errorf("HelloWorld() output = %q, want %q", got, want)
	}
}
