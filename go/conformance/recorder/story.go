package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"hop.top/kit/go/conformance/scenario"
)

// ResolveStory locates and reads the story source for a scenario.
// Resolution order:
//
//  1. explicit, when non-empty (a direct file path; must exist).
//  2. storyPath as an absolute path.
//  3. storyPath relative to the scenario file's directory, then each
//     ancestor directory up to the filesystem root. This matches the
//     common library layout where scenario.yaml lives at
//     scenarios/<ns>/<id>/<version>/ and story_path points relative
//     to the library root (e.g. "stories/<id>.yaml").
//
// Returns the story bytes and the path they were read from. An
// unresolvable story returns a *StoryNotFoundError.
func ResolveStory(scenarioPath, storyPath, explicit string) ([]byte, string, error) {
	if explicit != "" {
		b, err := os.ReadFile(explicit)
		if err != nil {
			return nil, "", &StoryNotFoundError{StoryPath: explicit, Err: err}
		}
		return b, explicit, nil
	}
	if storyPath == "" {
		return nil, "", &StoryNotFoundError{StoryPath: storyPath,
			Err: fmt.Errorf("scenario declares no story_ref.story_path and no explicit story was given")}
	}
	if filepath.IsAbs(storyPath) {
		b, err := os.ReadFile(storyPath)
		if err != nil {
			return nil, "", &StoryNotFoundError{StoryPath: storyPath, Err: err}
		}
		return b, storyPath, nil
	}

	absScenario, err := filepath.Abs(scenarioPath)
	if err != nil {
		return nil, "", fmt.Errorf("recorder: abs scenario path: %w", err)
	}
	var tried []string
	dir := filepath.Dir(absScenario)
	for {
		candidate := filepath.Join(dir, filepath.FromSlash(storyPath))
		if b, err := os.ReadFile(candidate); err == nil {
			return b, candidate, nil
		}
		tried = append(tried, candidate)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", &StoryNotFoundError{StoryPath: storyPath,
		Err: fmt.Errorf("not found relative to the scenario file or any ancestor (tried %d locations)", len(tried))}
}

// DeriveRef derives the manifest's namespace-qualified scenario_id
// and scenario_version from a library-shaped scenario path:
//
//	.../scenarios/<ns>/<id>/<version>/scenario.yaml
//
// The derivation only fires when the path's <id> segment matches the
// document's scenario_id; otherwise the bare scenario_id is returned
// with no version, and the caller (or `grade --scenario-id`) supplies
// the ref explicitly.
func DeriveRef(scenarioPath string, sc *scenario.Scenario) (id, version string) {
	if sc == nil {
		return "", ""
	}
	id = sc.ScenarioID
	abs, err := filepath.Abs(scenarioPath)
	if err != nil {
		return id, ""
	}
	parts := strings.Split(filepath.ToSlash(abs), "/")
	// Expect [... scenarios ns id version scenario.yaml].
	if len(parts) < 5 {
		return id, ""
	}
	last := parts[len(parts)-1]
	ver := parts[len(parts)-2]
	pid := parts[len(parts)-3]
	ns := parts[len(parts)-4]
	marker := parts[len(parts)-5]
	if marker != "scenarios" || last != "scenario.yaml" {
		return id, ""
	}
	if pid != sc.ScenarioID || ns == "" || ver == "" {
		return id, ""
	}
	return ns + "/" + pid, ver
}
