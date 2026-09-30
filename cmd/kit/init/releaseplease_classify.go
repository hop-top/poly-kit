// Package kitinit — releaseplease_classify.go recognizes workflows that
// run release-please and decides whether one is "plain": a single job
// whose whole behavior the kit release-please caller reproduces
// (triggers, config paths, target branch). Only a plain workflow may be
// migrated; anything else (extra jobs, outputs, run steps, unknown
// action inputs, other triggers) is custom and never rewritten.
package kitinit

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// releasePleaseActionPrefixes are the `uses:` prefixes of the upstream
// release-please action (current and pre-2023 org).
var releasePleaseActionPrefixes = []string{
	"googleapis/release-please-action@",
	"google-github-actions/release-please-action@",
}

// releasePleaseReusablePrefix is the `uses:` prefix of the hop-top
// reusable workflow the kit caller calls (any ref).
const releasePleaseReusablePrefix = "hop-top/.github/.github/workflows/" + releasePleaseReusable + "@"

// release-please-action's own defaults when config-file / manifest-file
// are omitted.
const (
	actionDefaultConfig   = "release-please-config.json"
	actionDefaultManifest = ".release-please-manifest.json"
)

// releasePleaseWorkflow is one workflow file that runs release-please.
type releasePleaseWorkflow struct {
	RelPath string
	// Plain: the kit caller built from Settings reproduces it.
	Plain bool
	// KitCaller: the file calls the hop-top reusable workflow (a kit
	// caller, possibly hand-copied or hand-edited) rather than running
	// the action itself.
	KitCaller bool
	// Why lists what makes a non-plain workflow custom.
	Why      string
	Settings releasePleaseSettings
	// Token names how a hand-written job authenticated release-please:
	// "the release-bot App", "secrets.<NAME>" or "GITHUB_TOKEN". Empty
	// for a kit caller.
	Token string
}

var exprRe = regexp.MustCompile(`^\$\{\{\s*(.*?)\s*\}\}$`)

// expr returns the trimmed inner expression of a `${{ ... }}` string
// and whether v was one.
func expr(v string) (string, bool) {
	m := exprRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// classifyReleasePleaseWorkflow reports whether data (the workflow at
// rel) runs release-please and, if so, whether it is plain. A file that
// does not parse but names the action is treated as custom: kit never
// rewrites what it cannot read.
func classifyReleasePleaseWorkflow(rel string, data []byte) (releasePleaseWorkflow, bool) {
	wf := releasePleaseWorkflow{RelPath: rel}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil || doc == nil {
		if bytes.Contains(data, []byte("release-please-action@")) ||
			bytes.Contains(data, []byte(releasePleaseReusablePrefix)) {
			wf.Why = "not parseable as a workflow"
			return wf, true
		}
		return wf, false
	}
	jobs, _ := doc["jobs"].(map[string]any)
	rpJob := ""
	for _, name := range sortedKeys(jobs) {
		if jobRunsReleasePlease(jobs[name]) {
			rpJob = name
			break
		}
	}
	if rpJob == "" {
		return wf, false
	}

	var why []string
	for _, k := range sortedKeys(doc) {
		switch k {
		case "name", "run-name", "on", "permissions", "concurrency", "jobs":
		default:
			why = append(why, fmt.Sprintf("top-level `%s`", k))
		}
	}
	why = append(why, classifyTriggers(doc["on"], &wf.Settings)...)
	if len(jobs) > 1 {
		var extra []string
		for _, name := range sortedKeys(jobs) {
			if name != rpJob {
				extra = append(extra, name)
			}
		}
		why = append(why, "extra jobs: "+strings.Join(extra, ", "))
	}
	job, _ := jobs[rpJob].(map[string]any)
	if uses, _ := job["uses"].(string); uses != "" {
		wf.KitCaller = true
		why = append(why, classifyCallerJob(job, &wf.Settings)...)
	} else {
		why = append(why, classifyActionJob(job, &wf)...)
	}
	// Spelled-out defaults collapse so the caller omits them.
	if wf.Settings.ConfigFile == releasePleaseDefaultConfig {
		wf.Settings.ConfigFile = ""
	}
	if wf.Settings.ManifestFile == releasePleaseDefaultManifest {
		wf.Settings.ManifestFile = ""
	}
	wf.Why = strings.Join(why, "; ")
	wf.Plain = len(why) == 0
	return wf, true
}

// jobRunsReleasePlease: the job calls the reusable workflow or has a
// release-please-action step.
func jobRunsReleasePlease(v any) bool {
	job, _ := v.(map[string]any)
	if uses, _ := job["uses"].(string); strings.HasPrefix(uses, releasePleaseReusablePrefix) {
		return true
	}
	steps, _ := job["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		if uses, _ := step["uses"].(string); isReleasePleaseAction(uses) {
			return true
		}
	}
	return false
}

func isReleasePleaseAction(uses string) bool {
	for _, p := range releasePleaseActionPrefixes {
		if strings.HasPrefix(uses, p) {
			return true
		}
	}
	return false
}

// classifyTriggers accepts `push: {branches: [...]}` and a
// `workflow_dispatch` without inputs — the two triggers the caller
// renders.
func classifyTriggers(v any, s *releasePleaseSettings) []string {
	on, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("trigger `on: %v`", v)}
	}
	var why []string
	for _, k := range sortedKeys(on) {
		switch k {
		case "push":
			push, _ := on[k].(map[string]any)
			branches, _ := push["branches"].([]any)
			if len(push) != 1 || len(branches) == 0 {
				why = append(why, "push trigger other than a branch list")
				continue
			}
			for _, b := range branches {
				name, ok := b.(string)
				if !ok {
					why = append(why, "push trigger other than a branch list")
					break
				}
				s.Branches = append(s.Branches, name)
			}
		case "workflow_dispatch":
			if d, ok := on[k].(map[string]any); on[k] != nil && (!ok || len(d) > 0) {
				why = append(why, "workflow_dispatch inputs")
				continue
			}
			s.Dispatch = true
		default:
			why = append(why, fmt.Sprintf("trigger `%s`", k))
		}
	}
	if s.Branches == nil && !s.Dispatch && len(why) == 0 {
		why = append(why, "no push or workflow_dispatch trigger")
	}
	return why
}

// classifyCallerJob reads a job that calls the reusable workflow.
func classifyCallerJob(job map[string]any, s *releasePleaseSettings) []string {
	var why []string
	for _, k := range sortedKeys(job) {
		switch k {
		case "uses", "with", "secrets", "permissions", "name":
		default:
			why = append(why, fmt.Sprintf("caller job `%s`", k))
		}
	}
	with, _ := job["with"].(map[string]any)
	for _, k := range sortedKeys(with) {
		v, ok := with[k].(string)
		if !ok || strings.Contains(v, "${{") {
			why = append(why, fmt.Sprintf("caller input `%s` is not a literal", k))
			continue
		}
		switch k {
		case "config-file":
			s.ConfigFile = v
		case "manifest-file":
			s.ManifestFile = v
		case "target-branch":
			s.TargetBranch = v
		default:
			why = append(why, fmt.Sprintf("caller input `%s`", k))
		}
	}
	switch sec := job["secrets"].(type) {
	case string:
		if sec != "inherit" {
			why = append(why, "caller secrets")
		}
	case map[string]any:
		for _, k := range sortedKeys(sec) {
			v, _ := sec[k].(string)
			if e, _ := expr(v); e != "secrets."+k ||
				(k != "RELEASE_BOT_APP_ID" && k != "RELEASE_BOT_PRIVATE_KEY") {
				why = append(why, fmt.Sprintf("caller secret `%s`", k))
			}
		}
	default:
		why = append(why, "caller passes no release-bot secrets")
	}
	return why
}

// classifyActionJob reads a hand-written job running the action.
func classifyActionJob(job map[string]any, wf *releasePleaseWorkflow) []string {
	var why []string
	for _, k := range sortedKeys(job) {
		switch k {
		case "runs-on", "steps", "permissions", "name", "timeout-minutes", "concurrency":
		default:
			why = append(why, fmt.Sprintf("job `%s`", k))
		}
	}
	appTokenID := ""
	var rpWith map[string]any
	rpSteps := 0
	steps, _ := job["steps"].([]any)
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		for _, k := range sortedKeys(step) {
			switch k {
			case "uses", "with", "id", "name":
			default:
				why = append(why, fmt.Sprintf("step `%s`", k))
			}
		}
		uses, _ := step["uses"].(string)
		with, _ := step["with"].(map[string]any)
		switch {
		case uses == "":
		case strings.HasPrefix(uses, "actions/checkout@"):
		case strings.HasPrefix(uses, "actions/create-github-app-token@"):
			app, _ := with["app-id"].(string)
			key, _ := with["private-key"].(string)
			appE, _ := expr(app)
			keyE, _ := expr(key)
			if len(with) != 2 || appE != "secrets.RELEASE_BOT_APP_ID" || keyE != "secrets.RELEASE_BOT_PRIVATE_KEY" {
				why = append(why, "App token not minted from the release-bot secrets")
			}
			appTokenID, _ = step["id"].(string)
		case isReleasePleaseAction(uses):
			rpSteps++
			rpWith = with
		default:
			why = append(why, fmt.Sprintf("step `uses: %s`", uses))
		}
	}
	if rpSteps != 1 {
		why = append(why, fmt.Sprintf("%d release-please steps", rpSteps))
	}

	s := &wf.Settings
	s.ConfigFile, s.ManifestFile = actionDefaultConfig, actionDefaultManifest
	wf.Token = "GITHUB_TOKEN"
	for _, k := range sortedKeys(rpWith) {
		v, ok := rpWith[k].(string)
		if !ok {
			why = append(why, fmt.Sprintf("release-please-action input `%s`", k))
			continue
		}
		e, isExpr := expr(v)
		switch k {
		case "config-file", "manifest-file":
			if isExpr {
				why = append(why, fmt.Sprintf("release-please-action input `%s` is an expression", k))
			} else if k == "config-file" {
				s.ConfigFile = v
			} else {
				s.ManifestFile = v
			}
		case "target-branch":
			switch {
			case isExpr && e == "github.ref_name":
				// Same as the reusable workflow's default.
			case isExpr:
				why = append(why, "target-branch expression other than github.ref_name")
			default:
				s.TargetBranch = v
			}
		case "token":
			switch {
			case appTokenID != "" && e == "steps."+appTokenID+".outputs.token":
				wf.Token = "the release-bot App"
			case e == "github.token" || e == "secrets.GITHUB_TOKEN":
			case strings.HasPrefix(e, "secrets."):
				wf.Token = e
			default:
				why = append(why, "release-please token expression")
			}
		default:
			why = append(why, fmt.Sprintf("release-please-action input `%s`", k))
		}
	}
	return why
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
