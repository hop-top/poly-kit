package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
)

type authorJSON struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// pluginJSON is the Claude Code plugin manifest. The skill directory is the
// plugin root, so it carries no component keys.
type pluginJSON struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Author      authorJSON `json:"author"`
	Homepage    string     `json:"homepage"`
	Repository  string     `json:"repository"`
	License     string     `json:"license"`
	Keywords    []string   `json:"keywords"`
}

type skillEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	SourceURL   string `json:"source_url"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type skillsFile struct {
	Skills []skillEntry `json:"skills"`
}

type gymFile struct {
	Schema       string   `json:"schema"`
	AllowedTools []string `json:"allowed_tools"`
	DataClass    string   `json:"data_class"`
}

type marketSource struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	Path   string `json:"path"`
}

// marketEntry is one entry for the shared plugin marketplace. It carries no
// version, ref or sha: the plugin's own manifest is the version authority.
type marketEntry struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Source      marketSource `json:"source"`
	Homepage    string       `json:"homepage"`
	License     string       `json:"license"`
	Category    string       `json:"category"`
	Keywords    []string     `json:"keywords"`
}

const (
	skillsFileName  = "skills.json"
	entriesFileName = "claude-marketplace-entries.json"
)

func marshalJSON(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err) // plain structs of strings cannot fail
	}
	return b.Bytes()
}

func gymJSON() []byte {
	return marshalJSON(gymFile{Schema: "gym.skill/v1", AllowedTools: []string{}, DataClass: "public"})
}

func (g *gen) keywords(s *skill) []string {
	var out []string
	for _, k := range append(slices.Clone(g.ports.Keywords), s.Spec.Keywords...) {
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// releaseVersion is the version the release manifest records for a component.
func (g *gen) releaseVersion(s *skill) (string, error) {
	v := g.manifest[s.Comp.Dir]
	if v == "" {
		return "", fmt.Errorf("%s: the release manifest has no version for %q (component %s), needed for the first manifest of %s",
			g.o.Manifest, s.Comp.Dir, s.Spec.Component, s.Spec.Skill)
	}
	return v, nil
}

// existingJSON decodes a JSON file already on disk; false when it is absent.
func (g *gen) existingJSON(rel string, into any) (bool, error) {
	b, err := os.ReadFile(g.abs(rel))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, into); err != nil {
		return false, fmt.Errorf("%s: %w", rel, err)
	}
	return true, nil
}

// pluginManifest builds plugin.json. The version is written once from the
// release manifest and preserved afterwards.
func (g *gen) pluginManifest(s *skill) ([]byte, error) {
	rel := s.rel(".claude-plugin/plugin.json")
	var old struct {
		Version string `json:"version"`
	}
	if _, err := g.existingJSON(rel, &old); err != nil {
		return nil, err
	}
	version := old.Version
	if version == "" {
		var err error
		if version, err = g.releaseVersion(s); err != nil {
			return nil, err
		}
	}
	return marshalJSON(pluginJSON{
		Name:        s.FM.Name,
		Version:     version,
		Description: s.FM.Description,
		Author:      authorJSON{Name: g.ports.Author.Name, URL: g.ports.Author.URL},
		Homepage:    g.skillURL(s),
		Repository:  g.repoURL(s),
		License:     s.FM.License,
		Keywords:    g.keywords(s),
	}), nil
}

// channelFiles writes the repository-root files that list skills: skills.json
// and the marketplace entries. Both list only skills that exist, in
// ports.yaml order.
func (g *gen) channelFiles() error {
	var old skillsFile
	if _, err := g.existingJSON(skillsFileName, &old); err != nil {
		return err
	}
	prev := map[string]string{}
	for _, e := range old.Skills {
		prev[e.Name] = e.Version
	}
	list := skillsFile{Skills: []skillEntry{}}
	entries := []marketEntry{}
	var errs []error
	for _, s := range g.skills {
		if !s.Present {
			continue
		}
		v := prev[s.Spec.Skill]
		if v == "" {
			var err error
			if v, err = g.releaseVersion(s); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		list.Skills = append(list.Skills, skillEntry{
			Name: s.FM.Name, Version: v, SourceURL: g.repoURL(s) + ".git",
			Path: "skills/" + s.Spec.Skill, Description: s.FM.Description,
		})
		entries = append(entries, marketEntry{
			Name: s.FM.Name, Description: s.FM.Description,
			Source:   marketSource{Source: "git-subdir", URL: g.repoURL(s) + ".git", Path: "skills/" + s.Spec.Skill},
			Homepage: g.skillURL(s), License: s.FM.License, Category: g.ports.Category, Keywords: g.keywords(s),
		})
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	g.put(skillsFileName, marshalJSON(list), 0o644)
	g.put(entriesFileName, marshalJSON(entries), 0o644)
	return nil
}
