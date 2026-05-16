package svc

import (
	"context"
	"errors"
)

// ErrScenarioNotFound is returned when a scenario reference cannot be
// resolved by the store.
var ErrScenarioNotFound = errors.New("scenario: not found")

// ScenarioStore is the v1 storage seam for scenarios + their prompts.
// The filesystem driver in fsstore.go is the default; future drivers
// (S3, SQL, Embed) plug into the same shape without API churn.
type ScenarioStore interface {
	// Get loads and parses the scenario at ref. Returns
	// ErrScenarioNotFound when missing.
	Get(ctx context.Context, ref ScenarioRef) (*Scenario, error)

	// Meta returns metadata without loading the full body.
	Meta(ctx context.Context, ref ScenarioRef) (*ScenarioMeta, error)

	// Prompt resolves a judge prompt_ref within the scenario's directory
	// (e.g. "prompts/launch-dry-run.md").
	Prompt(ctx context.Context, ref ScenarioRef, promptRef string) (string, error)

	// List returns metadata for scenarios visible in ns.
	List(ctx context.Context, ns string) ([]ScenarioMeta, error)

	// Namespaces returns every visible namespace.
	Namespaces(ctx context.Context) ([]string, error)
}
