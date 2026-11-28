package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Options names the tool's inputs. Paths other than Root are relative to it.
type Options struct {
	Root       string
	Parity     string
	Publish    string
	Tiers      string
	Manifest   string
	Ports      string
	RequireAll bool
}

// DefaultOptions returns the repository's input locations under root.
func DefaultOptions(root string) Options {
	return Options{
		Root:     root,
		Parity:   "docs/adopters/reference/capability-parity.md",
		Publish:  ".github/workflows/publish.yml",
		Tiers:    "docs/contributors/sdk-support-tiers.md",
		Manifest: ".github/.release-please-manifest.json",
		Ports:    "internal/tools/skillsync/ports.yaml",
	}
}

// Output is one file the generator owns.
type Output struct {
	Path string // slash-separated, relative to the root
	Data []byte
	Mode os.FileMode
}

// Plan is everything a run would write.
type Plan struct {
	Outputs  []Output
	Expected int      // skills ports.yaml names
	Present  []string // skills whose SKILL.md exists
	Missing  []string
}

// skill is one skill ports.yaml names, and what is known about it on disk.
type skill struct {
	Spec    Spec
	Router  bool
	Comp    Component
	Dir     string // repository-relative skill directory
	Col     int    // parity column index; -1 for the router
	Present bool
	FM      Frontmatter
	Src     []byte
}

func (s *skill) rel(p string) string { return path.Join(s.Dir, p) }

// gen holds every parsed input of one run.
type gen struct {
	o        Options
	ports    *Ports
	pub      map[string]Component
	tiers    map[string]string
	par      *Parity
	manifest map[string]string
	colSpec  []Spec // port owning each parity column
	skills   []*skill
	outs     map[string]Output
}

// loadInputs parses and cross-checks everything except skill files.
func loadInputs(o Options, needParity bool) (*gen, error) {
	at := func(rel string) string { return filepath.Join(o.Root, filepath.FromSlash(rel)) }
	g := &gen{o: o, outs: map[string]Output{}}
	var err error
	if g.ports, err = loadPorts(at(o.Ports), o.Ports); err != nil {
		return nil, err
	}
	if g.pub, err = loadPublish(at(o.Publish), o.Publish); err != nil {
		return nil, err
	}
	if err := g.ports.checkAgainstPublish(g.pub); err != nil {
		return nil, fmt.Errorf("%s and %s disagree:\n%w", o.Ports, o.Publish, err)
	}
	g.buildSkills()
	if !needParity {
		return g, nil
	}
	if g.tiers, err = loadTiers(at(o.Tiers), o.Tiers); err != nil {
		return nil, err
	}
	pb, err := os.ReadFile(at(o.Parity))
	if err != nil {
		return nil, err
	}
	if g.par, err = parseParity(o.Parity, string(pb)); err != nil {
		return nil, err
	}
	if g.colSpec, err = g.ports.checkColumns(g.par.Columns); err != nil {
		return nil, err
	}
	labels := make([]string, len(g.colSpec))
	for i, s := range g.colSpec {
		labels[i] = s.Language
	}
	if err := g.par.checkSummary(labels); err != nil {
		return nil, err
	}
	for _, s := range g.skills {
		if s.Router {
			continue
		}
		for i, cs := range g.colSpec {
			if cs.Skill == s.Spec.Skill {
				s.Col = i
			}
		}
	}
	mb, err := os.ReadFile(at(o.Manifest))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(mb, &g.manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", o.Manifest, err)
	}
	return g, nil
}

func (g *gen) buildSkills() {
	add := func(sp Spec, router bool) {
		c := g.pub[sp.Component]
		g.skills = append(g.skills, &skill{
			Spec: sp, Router: router, Comp: c, Col: -1,
			Dir: path.Join(path.Clean(filepath.ToSlash(c.Dir)), "skills", sp.Skill),
		})
	}
	add(g.ports.Router, true)
	for _, sp := range g.ports.Ports {
		add(sp, false)
	}
}

// BuildPlan computes every output without touching the tree.
func BuildPlan(o Options) (*Plan, error) {
	g, err := loadInputs(o, true)
	if err != nil {
		return nil, err
	}
	if err := g.readSkills(); err != nil {
		return nil, err
	}
	pl := &Plan{Expected: len(g.skills)}
	for _, s := range g.skills {
		if s.Present {
			pl.Present = append(pl.Present, s.Spec.Skill)
		} else {
			pl.Missing = append(pl.Missing, s.Spec.Skill)
		}
	}
	if o.RequireAll && len(pl.Missing) > 0 {
		return nil, fmt.Errorf("--require-all: no SKILL.md for %s", strings.Join(pl.Missing, ", "))
	}
	if err := g.validatePorts(); err != nil {
		return nil, err
	}
	var errs []error
	for _, s := range g.skills {
		if !s.Present {
			continue
		}
		if err := g.skillOutputs(s); err != nil {
			errs = append(errs, err)
		}
	}
	if err := g.copyOutputs(); err != nil {
		errs = append(errs, err)
	}
	if err := g.channelFiles(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	for _, out := range g.outs {
		pl.Outputs = append(pl.Outputs, out)
	}
	sort.Slice(pl.Outputs, func(i, j int) bool { return pl.Outputs[i].Path < pl.Outputs[j].Path })
	return pl, nil
}

func (g *gen) abs(rel string) string { return filepath.Join(g.o.Root, filepath.FromSlash(rel)) }

// readSkills finds which skills exist and reads their frontmatter.
func (g *gen) readSkills() error {
	var errs []error
	for _, s := range g.skills {
		b, err := os.ReadFile(g.abs(s.rel("SKILL.md")))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		s.Present, s.Src = true, b
		at := s.rel("SKILL.md")
		if s.FM, err = parseFrontmatter(b); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", at, err))
			continue
		}
		if p := frontmatterProblem(s.Spec.Skill, s.FM); p != "" {
			errs = append(errs, fmt.Errorf("%s: %s", at, p))
		}
	}
	return errors.Join(errs...)
}

func (g *gen) put(rel string, data []byte, mode os.FileMode) {
	g.outs[rel] = Output{Path: rel, Data: data, Mode: mode}
}

func (g *gen) putText(rel, text string) { g.put(rel, []byte(text), 0o644) }

func (g *gen) skillOutputs(s *skill) error {
	manifest, err := g.pluginManifest(s)
	if err != nil {
		return err
	}
	g.put(s.rel(".claude-plugin/plugin.json"), manifest, 0o644)
	g.put(s.rel("gym.json"), gymJSON(), 0o644)
	if err := g.minVersion(s); err != nil {
		return err
	}
	vb, err := g.versionBlock(s)
	if err != nil {
		return err
	}
	if s.Router {
		g.putText(s.rel("references/capability-matrix.md"), g.capabilityMatrix())
		g.putText(s.rel("references/go-only.md"), g.goOnlyPage())
		return errors.Join(g.block(s, "ports", g.portsBlock()), g.block(s, "version", vb))
	}
	g.putText(s.rel("references/coverage.md"), g.coveragePage(s))
	g.putText(s.rel("references/other-ports.md"), g.otherPorts())
	return errors.Join(g.block(s, "coverage", g.coverageBlock(s)), g.block(s, "version", vb))
}

// minVersion writes metadata.min_version from ports.yaml into the SKILL.md.
func (g *gen) minVersion(s *skill) error {
	out, err := setMinVersion(s.Src, s.Spec.MinVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", s.rel("SKILL.md"), err)
	}
	s.Src = out
	g.put(s.rel("SKILL.md"), out, 0o644)
	return nil
}

// block replaces one managed block of the skill's SKILL.md. Blocks chain: each
// call starts from the text the previous one produced.
func (g *gen) block(s *skill, id, body string) error {
	out, err := replaceBlock(s.Src, id, body)
	if err != nil {
		return fmt.Errorf("%s: %w", s.rel("SKILL.md"), err)
	}
	s.Src = out
	g.put(s.rel("SKILL.md"), out, 0o644)
	return nil
}

// copyOutputs writes every ports.yaml copy into the skills that exist.
func (g *gen) copyOutputs() error {
	var errs []error
	for _, c := range g.ports.Copies {
		var targets []*skill
		for _, s := range g.skills {
			if !s.Present {
				continue
			}
			if slices.Contains(c.Skills, s.Spec.Skill) || (slices.Contains(c.Skills, "ports") && !s.Router) {
				targets = append(targets, s)
			}
		}
		if len(targets) == 0 {
			continue
		}
		data, err := os.ReadFile(g.abs(c.From))
		if err != nil {
			errs = append(errs, fmt.Errorf("copies: %w", err))
			continue
		}
		info, err := os.Stat(g.abs(c.From))
		if err != nil {
			errs = append(errs, fmt.Errorf("copies: %w", err))
			continue
		}
		for _, s := range targets {
			dst := s.rel(c.To)
			if prev, dup := g.outs[dst]; dup && string(prev.Data) != string(data) {
				errs = append(errs, fmt.Errorf("copies: %s is written by two entries with different content", dst))
				continue
			}
			g.put(dst, data, info.Mode().Perm())
		}
	}
	return errors.Join(errs...)
}

// Write writes every output whose content differs and returns their paths.
func (p *Plan) Write(root string) ([]string, error) {
	var wrote []string
	for _, out := range p.Outputs {
		dst := filepath.Join(root, filepath.FromSlash(out.Path))
		if old, err := os.ReadFile(dst); err == nil && string(old) == string(out.Data) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return wrote, err
		}
		if err := os.WriteFile(dst, out.Data, out.Mode); err != nil {
			return wrote, err
		}
		if err := os.Chmod(dst, out.Mode); err != nil {
			return wrote, err
		}
		wrote = append(wrote, out.Path)
	}
	return wrote, nil
}

// Drift returns the paths whose content on disk differs from the plan.
func (p *Plan) Drift(root string) ([]string, error) {
	var stale []string
	for _, out := range p.Outputs {
		old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(out.Path)))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err != nil || string(old) != string(out.Data) {
			stale = append(stale, out.Path)
		}
	}
	return stale, nil
}

// ExpectedSkill is one line of `expected` output.
type ExpectedSkill struct {
	Mirror    string
	Dir       string // skill directory in this repository
	MirrorDir string // the same skill inside the mirror
}

// Expected lists the skills of one component (all components when empty).
// A component with no skill yields nothing; an unknown one is an error.
func Expected(o Options, component string) ([]ExpectedSkill, error) {
	g, err := loadInputs(o, false)
	if err != nil {
		return nil, err
	}
	if component != "" {
		if _, ok := g.pub[component]; !ok {
			return nil, fmt.Errorf("unknown component %q (publish matrix has: %s)", component, strings.Join(sortedComponents(g.pub), ", "))
		}
	}
	var out []ExpectedSkill
	for _, s := range g.skills {
		if component == "" || s.Spec.Component == component {
			out = append(out, ExpectedSkill{Mirror: s.Comp.Mirror, Dir: s.Dir, MirrorDir: path.Join("skills", s.Spec.Skill)})
		}
	}
	return out, nil
}
