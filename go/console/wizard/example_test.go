package wizard_test

import (
	"context"
	"errors"
	"fmt"

	"hop.top/kit/go/console/wizard"
)

// ExampleRunHeadless drives a wizard with pre-supplied answers and reads
// the results back through the typed accessors.
func ExampleRunHeadless() {
	w, err := wizard.New(
		wizard.TextInput("name", "Project name").
			WithRequired().
			WithDefault("my-app").
			WithValidateText(func(s string) error {
				if len(s) < 3 {
					return errors.New("too short")
				}
				return nil
			}),
		wizard.Select("region", "Region", []wizard.Option{
			{Value: "us-east-1", Label: "US East"},
			{Value: "eu-west-1", Label: "EU West"},
		}),
		wizard.Confirm("verbose", "Verbose output?"),
	)
	if err != nil {
		panic(err)
	}

	results, err := wizard.RunHeadless(context.Background(), w, map[string]any{
		"name":    "my-project",
		"region":  "us-east-1",
		"verbose": true,
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(wizard.String(results, "name"))
	fmt.Println(wizard.Choice(results, "region"))
	fmt.Println(wizard.Bool(results, "verbose"))
	// Output:
	// my-project
	// us-east-1
	// true
}

// ExampleStep_WithWhen shows a step gated on a prior answer. A step whose
// predicate is false is skipped and does not count toward StepCount.
func ExampleStep_WithWhen() {
	w, err := wizard.New(
		wizard.Confirm("use_db", "Use a database?"),
		wizard.TextInput("db_host", "Database host").
			WithWhen("use_db", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
	)
	if err != nil {
		panic(err)
	}

	results, err := wizard.RunHeadless(context.Background(), w, map[string]any{
		"use_db": false,
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("db_host present: %v\n", results["db_host"] != nil)
	fmt.Println(w.StepCount())
	// Output:
	// db_host present: false
	// 1
}

// ExampleRunHeadless_defaults shows that a missing answer falls back to the
// step's DefaultValue.
func ExampleRunHeadless_defaults() {
	w, err := wizard.New(
		wizard.TextInput("name", "Project name").WithDefault("my-app"),
	)
	if err != nil {
		panic(err)
	}

	results, err := wizard.RunHeadless(context.Background(), w, map[string]any{})
	if err != nil {
		panic(err)
	}

	fmt.Println(wizard.String(results, "name"))
	// Output:
	// my-app
}
