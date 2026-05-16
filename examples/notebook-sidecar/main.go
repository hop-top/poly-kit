// Command notebook-sidecar is a note-taking CLI that delegates to kit serve.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"
)

type startup struct {
	Port          int    `json:"port"`
	PID           int    `json:"pid"`
	Token         string `json:"token"`
	ShutdownToken string `json:"shutdown_token"`
}

type document struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}

type note struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

var baseURL string
var shutdownToken string

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "notebook-sidecar-*")
	if err != nil {
		fatal("create tmpdir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	kitBin := os.Getenv("KIT_BIN")
	if kitBin == "" {
		kitBin = "kit"
	}

	cmd := exec.Command(kitBin, "serve", "--port", "0",
		"--data", filepath.Join(tmpDir, "data"), "--no-peer", "--no-sync")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fatal("pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		fatal("start kit serve: %v", err)
	}
	defer func() {
		shutdown()
		_ = cmd.Wait()
	}()

	var info startup
	dec := json.NewDecoder(stdout)
	if err := dec.Decode(&info); err != nil {
		fatal("read startup json: %v", err)
	}
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", info.Port)
	shutdownToken = info.ShutdownToken

	switch os.Args[1] {
	case "new":
		doNew(os.Args[2:])
	case "list":
		doList()
	case "get":
		doGet(os.Args[2:])
	case "edit":
		doEdit(os.Args[2:])
	case "delete":
		doDelete(os.Args[2:])
	case "history":
		doHistory(os.Args[2:])
	case "revert":
		doRevert(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func doNew(args []string) {
	if len(args) < 1 {
		fatal("usage: notebook-sidecar new <title> [body]")
	}
	n := note{Title: args[0]}
	if len(args) > 1 {
		n.Body = args[1]
	}
	body, _ := json.Marshal(n)
	resp := request("POST", "/notes/", body)
	var doc document
	if err := json.Unmarshal(resp, &doc); err != nil {
		fatal("unmarshal: %v", err)
	}
	fmt.Printf("Created note %s\n", doc.ID)
}

func doList() {
	resp := request("GET", "/notes/", nil)
	var docs []document
	if err := json.Unmarshal(resp, &docs); err != nil {
		fatal("unmarshal: %v", err)
	}
	if len(docs) == 0 {
		fmt.Println("No notes found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tUPDATED")
	for _, d := range docs {
		var n note
		if err := json.Unmarshal(d.Data, &n); err != nil {
			fatal("unmarshal: %v", err)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", d.ID, n.Title, d.UpdatedAt)
	}
	w.Flush()
}

func doGet(args []string) {
	if len(args) < 1 {
		fatal("usage: notebook-sidecar get <id>")
	}
	resp := request("GET", "/notes/"+args[0], nil)
	var doc document
	if err := json.Unmarshal(resp, &doc); err != nil {
		fatal("unmarshal: %v", err)
	}
	var n note
	if err := json.Unmarshal(doc.Data, &n); err != nil {
		fatal("unmarshal: %v", err)
	}
	fmt.Printf("ID:      %s\nTitle:   %s\nBody:    %s\nCreated: %s\nUpdated: %s\n",
		doc.ID, n.Title, n.Body, doc.CreatedAt, doc.UpdatedAt)
}

func doEdit(args []string) {
	if len(args) < 2 {
		fatal("usage: notebook-sidecar edit <id> <title> [body]")
	}
	n := note{Title: args[1]}
	if len(args) > 2 {
		n.Body = args[2]
	}
	body, _ := json.Marshal(n)
	request("PUT", "/notes/"+args[0], body)
	fmt.Printf("Updated note %s\n", args[0])
}

func doDelete(args []string) {
	if len(args) < 1 {
		fatal("usage: notebook-sidecar delete <id>")
	}
	request("DELETE", "/notes/"+args[0], nil)
	fmt.Printf("Deleted note %s\n", args[0])
}

func doHistory(args []string) {
	if len(args) < 1 {
		fatal("usage: notebook-sidecar history <id>")
	}
	resp := request("GET", "/notes/"+args[0]+"/versions", nil)
	var versions []map[string]any
	if err := json.Unmarshal(resp, &versions); err != nil {
		fatal("unmarshal: %v", err)
	}
	if len(versions) == 0 {
		fmt.Println("No versions found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tTIMESTAMP")
	for _, v := range versions {
		fmt.Fprintf(w, "%v\t%v\n", v["id"], v["timestamp"])
	}
	w.Flush()
}

func doRevert(args []string) {
	if len(args) < 2 {
		fatal("usage: notebook-sidecar revert <id> <version>")
	}
	request("POST", "/notes/"+args[0]+"/revert/"+args[1], nil)
	fmt.Printf("Reverted note %s to version %s\n", args[0], args[1])
}

func shutdown() {
	req, _ := http.NewRequest("POST", baseURL+"/shutdown", nil)
	req.Header.Set("Authorization", "Bearer "+shutdownToken)
	http.DefaultClient.Do(req) //nolint:errcheck
}

func request(method, path string, body []byte) []byte {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, baseURL+path, r)
	if err != nil {
		fatal("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal("http: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		fatal("%s %s: %d %s", method, path, resp.StatusCode, string(data))
	}
	return data
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: notebook-sidecar <command> [args]")
	fmt.Fprintln(os.Stderr, "Commands: new, list, get, edit, delete, history, revert")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
