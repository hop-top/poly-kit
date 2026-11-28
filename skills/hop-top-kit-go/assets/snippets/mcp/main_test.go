package main

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMCPServesTheTree speaks MCP over the process's stdio, the way a
// desktop host does, and compares the greet tool's answer with
// mcp.expected. The service takes os.Stdin and os.Stdout at start, so the
// test swaps them for pipes first.
func TestMCPServesTheTree(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = origIn, origOut }()

	root := newRoot()
	root.SetArgs([]string{"service", "start", "mcp", "--stdio"})
	done := make(chan error, 1)
	go func() { done <- root.Execute(t.Context()) }()

	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	next := func() string {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatal("the service closed its output")
			}
			return l
		case <-time.After(10 * time.Second):
			t.Fatal("no message from the service")
			return ""
		}
	}
	send := func(msg string) {
		if _, err := inW.WriteString(msg + "\n"); err != nil {
			t.Fatal(err)
		}
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"host","version":"1"}}}`)
	next()
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"greet","arguments":{"args":["Ada"]}}}`)
	got := next()

	want, err := os.ReadFile("mcp.expected")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
		t.Fatalf("reply differs\n got: %s\nwant: %s", got, want)
	}

	_ = inW.Close() // end of input ends the session
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("service: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not stop at end of input")
	}
}
