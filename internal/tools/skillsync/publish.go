package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Component is one entry of the publish workflow's ecosystems map.
type Component struct {
	Name      string
	Dir       string `yaml:"dir"`
	Ecosystem string `yaml:"ecosystem"`
	Package   string `yaml:"package"`
	Mirror    string `yaml:"mirror"`
}

type workflow struct {
	Jobs map[string]struct {
		With struct {
			Ecosystems string `yaml:"ecosystems"`
		} `yaml:"with"`
	} `yaml:"jobs"`
}

// loadPublish reads the ecosystems map out of the publish workflow.
func loadPublish(file, display string) (map[string]Component, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var wf workflow
	if err := yaml.Unmarshal(b, &wf); err != nil {
		return nil, fmt.Errorf("%s: %w", display, err)
	}
	var blocks []string
	for _, j := range wf.Jobs {
		if strings.TrimSpace(j.With.Ecosystems) != "" {
			blocks = append(blocks, j.With.Ecosystems)
		}
	}
	if len(blocks) != 1 {
		return nil, fmt.Errorf("%s: want exactly one job with an ecosystems input, found %d", display, len(blocks))
	}
	var raw map[string]Component
	if err := yaml.Unmarshal([]byte(blocks[0]), &raw); err != nil {
		return nil, fmt.Errorf("%s: ecosystems: %w", display, err)
	}
	out := map[string]Component{}
	for name, c := range raw {
		if c.Dir == "" || c.Mirror == "" {
			return nil, fmt.Errorf("%s: component %q needs dir and mirror", display, name)
		}
		c.Name = name
		out[name] = c
	}
	return out, nil
}

func sortedComponents(m map[string]Component) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
