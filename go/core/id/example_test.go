package id_test

import (
	"encoding/json"
	"fmt"

	"hop.top/kit/go/core/id"
)

// taskID is the idiomatic kit pattern for a per-entity typed id: a
// zero-sized "prefix carrier" and a Typed[T] alias.
type taskID struct{}

func (taskID) Prefix() string { return "task" }

// TaskID is the entity-level type a kit-using CLI would expose.
type TaskID = id.Typed[taskID]

func ExampleNew() {
	s, err := id.New("task")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	// The canonical string starts with the prefix.
	fmt.Println(len(s) > len("task_") && s[:5] == "task_")
	// Output: true
}

func ExampleParse() {
	parsed, err := id.Parse("task_01jg000000e008000000000000")
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(parsed.Prefix)
	fmt.Println(parsed.UUID)
	// Output:
	// task
	// 01940000-0000-7000-8000-000000000000
}

func ExampleTyped() {
	// JSON round-trip uses the bare canonical string as the wire form.
	type todo struct {
		ID    TaskID `json:"id"`
		Title string `json:"title"`
	}

	t := todo{
		ID:    id.MustNewTyped[taskID](),
		Title: "ship typeid",
	}

	// Recover the canonical string and the underlying uuid.
	fmt.Println(t.ID.Prefix())

	b, _ := json.Marshal(struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}{ID: "task_01jg000000e008000000000000", Title: "ship typeid"})

	var got todo
	_ = json.Unmarshal(b, &got)
	fmt.Println(got.ID.String())
	// Output:
	// task
	// task_01jg000000e008000000000000
}
