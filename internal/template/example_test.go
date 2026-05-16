package template_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing/fstest"

	"hop.top/kit/internal/template"
)

// ExampleNewEngineWithRules renders a single .tmpl file from an
// in-memory fs.FS into a temp directory, demonstrating the minimal
// Engine usage with a render_rules block declaring the strip suffix.
// Output shows the basename of the written file plus the rendered
// contents.
func ExampleNewEngineWithRules() {
	src := fstest.MapFS{
		"main.go.tmpl": &fstest.MapFile{
			Data: []byte("package main // {{.Name}}\n"),
		},
	}
	target, _ := os.MkdirTemp("", "kit-engine-example-*")
	defer os.RemoveAll(target)

	rules := template.RenderRules{StripSuffixes: []string{".tmpl"}}
	eng := template.NewEngineWithRules(src, target,
		map[string]any{"Name": "demo"},
		template.FileRules{},
		rules,
		nil,   // no tier filter
		0,     // bootstrap mode
		false, // no force
	)
	result, _ := eng.Render(context.Background())

	for _, p := range result.Written {
		fmt.Println("written:", filepath.Base(p))
	}

	data, _ := os.ReadFile(filepath.Join(target, "main.go"))
	fmt.Print(string(data))

	// Output:
	// written: main.go
	// package main // demo
}

// ExampleNewRegistry resolves the built-in cli-go template via the
// embed.FS-backed Registry, then reads its manifest from the resulting
// fs.FS to confirm a successful resolve.
func ExampleNewRegistry() {
	reg := template.NewRegistry("", "")
	srcFS, err := reg.Resolve(context.Background(), "cli-go")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	data, _ := fs.ReadFile(srcFS, "kit-template.yaml")
	if len(data) > 0 {
		fmt.Println("cli-go manifest found")
	}

	// Output:
	// cli-go manifest found
}
