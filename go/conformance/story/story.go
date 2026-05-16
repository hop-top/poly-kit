package story

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/conformance/story/parser"
	"hop.top/kit/go/conformance/story/schema"
)

// Discover walks the directory rooted at dir and returns every .yaml
// / .yml file. Hidden directories (starting with ".") are pruned so
// adopter repos with .git / .tlc / .github don't pull noise.
//
// Errors short-circuit the walk; callers get the partial list and
// the error so they can decide whether to render or bail.
func Discover(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			name := d.Name()
			if p != dir && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".yaml" || ext == ".yml" {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

// Index parses every .yaml / .yml story under storiesDir and returns
// a map of story_id → *schema.Story. Stories that fail to parse are
// reported via the returned error; the map contains the successful
// subset.
//
// Duplicate ids are reported as well; the map retains the first
// occurrence under that id. Scenario tooling that wants the full
// shape can call this and post-process.
func Index(storiesDir string) (map[string]*schema.Story, error) {
	paths, walkErr := Discover(storiesDir)
	idx := make(map[string]*schema.Story, len(paths))
	var firstErr error
	for _, p := range paths {
		ps, err := parser.ParseFile(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if ps.Story == nil || ps.Story.StoryID == "" {
			continue
		}
		if _, exists := idx[ps.Story.StoryID]; exists {
			continue
		}
		idx[ps.Story.StoryID] = ps.Story
	}
	if firstErr != nil {
		return idx, firstErr
	}
	return idx, walkErr
}

// ContentSHA256 returns a stable hex-encoded SHA-256 of the story's
// normalized content. Normalization re-marshals the parsed struct
// through yaml.v3 with sorted map keys, which collapses formatting /
// comment / order differences. This lets a scenario pin a story by
// content while tolerating whitespace edits.
//
// Two stories with identical fields (modulo formatting + comments)
// produce identical digests. Adopters that want strict pinning call
// this at authoring time and store the result in the scenario.
func ContentSHA256(s *schema.Story) (string, error) {
	if s == nil {
		return "", fmt.Errorf("story.ContentSHA256: nil story")
	}
	// Re-marshal through yaml.v3. The default encoder sorts struct
	// fields by their declaration order, which is stable across
	// runs for the typed Story struct. Map[string]any (metadata) is
	// sorted by the encoder.
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		return "", fmt.Errorf("story.ContentSHA256: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("story.ContentSHA256: close: %w", err)
	}
	sum := sha256.Sum256([]byte(buf.String()))
	return hex.EncodeToString(sum[:]), nil
}

// ReadStory is a convenience that combines ReadFile + ParseBytes.
// Returns the typed Story; callers needing line-number information
// should use parser.ParseFile directly.
func ReadStory(path string) (*schema.Story, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ps, err := parser.ParseBytes(data, path)
	if err != nil {
		return nil, err
	}
	return ps.Story, nil
}
