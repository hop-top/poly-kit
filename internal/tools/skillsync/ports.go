package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is one skill entry of ports.yaml: the router, or a port.
type Spec struct {
	Component string   `yaml:"component"`
	Skill     string   `yaml:"skill"`
	Column    string   `yaml:"column"`    // parity table column; ports only
	Language  string   `yaml:"language"`  // Summary label and prose name; ports only
	Install   string   `yaml:"install"`   // no version number
	Package   string   `yaml:"package"`   // only when publish.yml names none
	Tier      string   `yaml:"tier"`      // only when the tiers page has no row
	Toolchain string   `yaml:"toolchain"` // floor
	Keywords  []string `yaml:"keywords"`
}

// CopySpec copies one repository file into skills.
type CopySpec struct {
	From   string   `yaml:"from"`   // repository-relative source
	To     string   `yaml:"to"`     // destination inside a skill
	Skills []string `yaml:"skills"` // skill names, or the single word "ports"
}

// Ports is the hand-written ports.yaml.
type Ports struct {
	Docs struct {
		Repo   string `yaml:"repo"`
		Branch string `yaml:"branch"`
	} `yaml:"docs"`
	Host         string `yaml:"host"`
	MirrorBranch string `yaml:"mirror_branch"`
	Author       struct {
		Name string `yaml:"name"`
		URL  string `yaml:"url"`
	} `yaml:"author"`
	Category        string     `yaml:"category"`
	Keywords        []string   `yaml:"keywords"`
	ReferenceColumn string     `yaml:"reference_column"`
	Router          Spec       `yaml:"router"`
	Ports           []Spec     `yaml:"ports"`
	NoSkill         []string   `yaml:"no_skill"`
	Copies          []CopySpec `yaml:"copies"`
}

func loadPorts(file, display string) (*Ports, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var p Ports
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("%s: %w", display, err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", display, err)
	}
	return &p, nil
}

func (p *Ports) validate() error {
	var errs []error
	need := func(v, what string) {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is empty", what))
		}
	}
	need(p.Docs.Repo, "docs.repo")
	need(p.Docs.Branch, "docs.branch")
	need(p.Host, "host")
	need(p.MirrorBranch, "mirror_branch")
	need(p.Author.Name, "author.name")
	need(p.Category, "category")
	need(p.ReferenceColumn, "reference_column")
	if !strings.HasPrefix(p.Host, "https://") || strings.HasSuffix(p.Host, "/") {
		errs = append(errs, fmt.Errorf("host %q must be an https URL without a trailing slash", p.Host))
	}
	need(p.Router.Skill, "router.skill")
	need(p.Router.Component, "router.component")
	names := map[string]bool{p.Router.Skill: true}
	cols := map[string]bool{}
	for i, s := range p.Ports {
		at := fmt.Sprintf("ports[%d] (%s)", i, s.Skill)
		for _, f := range [][2]string{{s.Component, "component"}, {s.Skill, "skill"}, {s.Column, "column"}, {s.Language, "language"}, {s.Install, "install"}, {s.Toolchain, "toolchain"}} {
			if strings.TrimSpace(f[0]) == "" {
				errs = append(errs, fmt.Errorf("%s: %s is empty", at, f[1]))
			}
		}
		if names[s.Skill] {
			errs = append(errs, fmt.Errorf("%s: skill name used twice", at))
		}
		names[s.Skill] = true
		if cols[s.Column] {
			errs = append(errs, fmt.Errorf("%s: column %q claimed twice", at, s.Column))
		}
		cols[s.Column] = true
	}
	if !cols[p.ReferenceColumn] {
		errs = append(errs, fmt.Errorf("reference_column %q is not a port column", p.ReferenceColumn))
	}
	for i, c := range p.Copies {
		if c.From == "" || c.To == "" || len(c.Skills) == 0 {
			errs = append(errs, fmt.Errorf("copies[%d]: from, to and skills are all required", i))
			continue
		}
		for _, f := range []string{c.From, c.To} {
			if path.IsAbs(f) || path.Clean(f) != f || strings.HasPrefix(f, "..") {
				errs = append(errs, fmt.Errorf("copies[%d]: %q must be a clean relative path inside its tree", i, f))
			}
		}
		for _, s := range c.Skills {
			if s != "ports" && !names[s] {
				errs = append(errs, fmt.Errorf("copies[%d]: unknown skill %q", i, s))
			}
		}
	}
	return errors.Join(errs...)
}

// components returns every component ports.yaml gives a skill.
func (p *Ports) components() []string {
	seen := map[string]bool{p.Router.Component: true}
	out := []string{p.Router.Component}
	for _, s := range p.Ports {
		if !seen[s.Component] {
			seen[s.Component] = true
			out = append(out, s.Component)
		}
	}
	return out
}

// checkAgainstPublish fails, naming each component, when publish.yml and
// ports.yaml disagree about which components exist or have skills.
func (p *Ports) checkAgainstPublish(pub map[string]Component) error {
	var errs []error
	have := p.components()
	for _, n := range p.NoSkill {
		if slices.Contains(have, n) {
			errs = append(errs, fmt.Errorf("component %q is in both ports and no_skill", n))
		}
		if _, ok := pub[n]; !ok {
			errs = append(errs, fmt.Errorf("no_skill names %q, which the publish matrix does not have", n))
		}
	}
	for _, n := range have {
		if _, ok := pub[n]; !ok {
			errs = append(errs, fmt.Errorf("ports.yaml names component %q, which the publish matrix does not have", n))
		}
	}
	for _, n := range sortedComponents(pub) {
		if !slices.Contains(have, n) && !slices.Contains(p.NoSkill, n) {
			errs = append(errs, fmt.Errorf("publish matrix component %q (mirror %s) is in neither ports.yaml nor no_skill: decide whether it gets a skill", n, pub[n].Mirror))
		}
	}
	return errors.Join(errs...)
}

// checkColumns requires the ports' columns to be exactly the page's SDK
// columns, and returns, per page column, the port that owns it.
func (p *Ports) checkColumns(page []string) ([]Spec, error) {
	var errs []error
	out := make([]Spec, len(page))
	for i, c := range page {
		found := false
		for _, s := range p.Ports {
			if s.Column == c {
				out[i], found = s, true
			}
		}
		if !found {
			errs = append(errs, fmt.Errorf("parity column %q is claimed by no port in ports.yaml", c))
		}
	}
	for _, s := range p.Ports {
		if !slices.Contains(page, s.Column) {
			errs = append(errs, fmt.Errorf("ports.yaml column %q (%s) is not a parity column", s.Column, s.Skill))
		}
	}
	return out, errors.Join(errs...)
}
