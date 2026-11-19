package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/config"
)

type rulesCfg struct {
	Name  string `yaml:"name"`
	Owner string `yaml:"owner_profile"`
	Bos   struct {
		URL string `yaml:"url"`
	} `yaml:"bos"`
	AI struct {
		Token  string `yaml:"token"`
		Models struct {
			Allow []string `yaml:"allow"`
			Deny  []string `yaml:"deny"`
		} `yaml:"models"`
	} `yaml:"ai"`
}

func writeRaw(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func globMatch(pattern, value string) bool {
	ok, _ := filepath.Match(pattern, value)
	return ok
}

func loadRules(t *testing.T, rules *config.ProjectRules, system, user, project string) (rulesCfg, error) {
	t.Helper()
	var cfg rulesCfg
	err := config.Load(&cfg, config.Options{
		SystemConfigPath:  system,
		UserConfigPath:    user,
		ProjectConfigPath: project,
		Project:           rules,
	})
	return cfg, err
}

func TestProjectRules_ForbiddenExact(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "owner_profile: evil\n")
	user := filepath.Join(t.TempDir(), "user", "config.yaml")
	_, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"owner_profile"}}, "", user, proj)
	require.Error(t, err)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe))
	assert.Equal(t, proj, fe.File)
	assert.Equal(t, "owner_profile", fe.Key)
	assert.Contains(t, fe.Suggestion, user)
	assert.Contains(t, err.Error(), proj)
	assert.Contains(t, err.Error(), "owner_profile")
	assert.Contains(t, err.Error(), user)
}

func TestProjectRules_ForbiddenPrefix(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "bos:\n  url: http://x\n")
	_, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"bos"}}, "", "", proj)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe))
	assert.Equal(t, "bos", fe.Key)
	assert.Contains(t, fe.Suggestion, "user config")
}

func TestProjectRules_ForbiddenNestedExact(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    deny: [a]\n")
	_, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"ai.models.deny"}}, "", "", proj)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe))
	assert.Equal(t, "ai.models.deny", fe.Key)
}

func TestProjectRules_ForbiddenPrefixNotSubstring(t *testing.T) {
	// "bos" must not forbid "bosun".
	proj := writeRaw(t, "p.yaml", "name: bosun\n")
	cfg, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"bos", "nam"}}, "", "", proj)
	require.NoError(t, err)
	assert.Equal(t, "bosun", cfg.Name)
}

func TestProjectRules_SecretPattern(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  token: abc\n")
	rules := &config.ProjectRules{SecretKey: regexp.MustCompile(`(?i)secret|token`)}
	_, err := loadRules(t, rules, "", "", proj)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe))
	assert.Equal(t, "ai.token", fe.Key)
	assert.True(t, fe.Secret)
}

func TestProjectRules_SecretPatternMatchesSegmentNotWholeKey(t *testing.T) {
	// Anchored pattern matches a segment, not the dotted path.
	proj := writeRaw(t, "p.yaml", "ai:\n  token: abc\n")
	rules := &config.ProjectRules{SecretKey: regexp.MustCompile(`^ai\.token$`)}
	_, err := loadRules(t, rules, "", "", proj)
	require.NoError(t, err)
}

func TestProjectRules_ForbiddenAllowedInUserFile(t *testing.T) {
	user := writeRaw(t, "u.yaml", "owner_profile: me\nbos:\n  url: u\nai:\n  token: t\n")
	rules := &config.ProjectRules{
		Forbidden: []string{"owner_profile", "bos"},
		SecretKey: regexp.MustCompile(`(?i)token`),
	}
	cfg, err := loadRules(t, rules, "", user, "")
	require.NoError(t, err)
	assert.Equal(t, "me", cfg.Owner)
	assert.Equal(t, "u", cfg.Bos.URL)
	assert.Equal(t, "t", cfg.AI.Token)
}

func TestProjectRules_ForbiddenAllowedInSystemExtraEnvOverrides(t *testing.T) {
	sys := writeRaw(t, "s.yaml", "owner_profile: sys\n")
	extra := writeRaw(t, "x.yaml", "bos:\n  url: extra\n")
	rules := &config.ProjectRules{Forbidden: []string{"owner_profile", "bos", "ai.token"}}
	var cfg rulesCfg
	err := config.Load(&cfg, config.Options{
		SystemConfigPath: sys,
		ExtraConfigPaths: []string{extra},
		EnvOverride:      func(c any) { c.(*rulesCfg).AI.Token = "env" },
		Overrides:        map[string]any{"owner_profile": "flag"},
		Project:          rules,
	})
	require.NoError(t, err)
	assert.Equal(t, "flag", cfg.Owner)
	assert.Equal(t, "extra", cfg.Bos.URL)
	assert.Equal(t, "env", cfg.AI.Token)
}

func TestProjectRules_NarrowRulesIgnoredOutsideProject(t *testing.T) {
	// A user file's list replaces the system list as usual.
	sys := writeRaw(t, "s.yaml", "ai:\n  models:\n    allow: [a, b]\n")
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    allow: [c]\n")
	rules := &config.ProjectRules{Narrow: []config.NarrowKey{
		{Key: "ai.models.allow", Mode: config.Intersect},
	}}
	cfg, err := loadRules(t, rules, sys, user, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, cfg.AI.Models.Allow)
}

func TestProjectRules_NilRulesUnchanged(t *testing.T) {
	user := writeRaw(t, "u.yaml", "name: u\nai:\n  models:\n    allow: [a]\n")
	proj := writeRaw(t, "p.yaml", "owner_profile: p\nai:\n  models:\n    allow: [z]\n")
	cfg, err := loadRules(t, nil, "", user, proj)
	require.NoError(t, err)
	assert.Equal(t, "u", cfg.Name)
	assert.Equal(t, "p", cfg.Owner)
	assert.Equal(t, []string{"z"}, cfg.AI.Models.Allow)
}

func TestProjectRules_EmptyRulesMergeAsBefore(t *testing.T) {
	user := writeRaw(t, "u.yaml", "name: u\nai:\n  models:\n    allow: [a]\n")
	proj := writeRaw(t, "p.yaml", "name: p\nai:\n  models:\n    allow: [z]\n")
	cfg, err := loadRules(t, &config.ProjectRules{}, "", user, proj)
	require.NoError(t, err)
	assert.Equal(t, "p", cfg.Name)
	assert.Equal(t, []string{"z"}, cfg.AI.Models.Allow)
}

func TestProjectRules_MissingProjectFileSkipped(t *testing.T) {
	_, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"bos"}}, "", "",
		filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
}

func TestProjectRules_PrecedenceEnvBeatsProject(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "name: project\n")
	var cfg rulesCfg
	err := config.Load(&cfg, config.Options{
		ProjectConfigPath: proj,
		EnvOverride:       func(c any) { c.(*rulesCfg).Name = "env" },
		Project:           &config.ProjectRules{Forbidden: []string{"bos"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "env", cfg.Name)
}

func TestProjectRules_ExtraFileBeatsNarrowedProject(t *testing.T) {
	sys := writeRaw(t, "s.yaml", "ai:\n  models:\n    allow: [a, b]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: [a]\n")
	extra := writeRaw(t, "x.yaml", "ai:\n  models:\n    allow: [q]\n")
	var cfg rulesCfg
	err := config.Load(&cfg, config.Options{
		SystemConfigPath:  sys,
		ProjectConfigPath: proj,
		ExtraConfigPaths:  []string{extra},
		Project: &config.ProjectRules{Narrow: []config.NarrowKey{
			{Key: "ai.models.allow", Mode: config.Intersect},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"q"}, cfg.AI.Models.Allow)
}

func narrow(key string, mode config.NarrowMode, m func(string, string) bool) *config.ProjectRules {
	return &config.ProjectRules{Narrow: []config.NarrowKey{{Key: key, Mode: mode, Match: m}}}
}

func TestProjectRules_UnionAddsToLower(t *testing.T) {
	sys := writeRaw(t, "s.yaml", "ai:\n  models:\n    deny: [a, b]\n")
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    deny: [c]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    deny: [b, d]\n")
	cfg, err := loadRules(t, narrow("ai.models.deny", config.Union, nil), sys, user, proj)
	require.NoError(t, err)
	// lower layers merge (user replaces system list), project adds.
	assert.Equal(t, []string{"c", "b", "d"}, cfg.AI.Models.Deny)
}

func TestProjectRules_UnionNoLowerListProjectStands(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    deny: [x, y, x]\n")
	cfg, err := loadRules(t, narrow("ai.models.deny", config.Union, nil), "", "", proj)
	require.NoError(t, err)
	assert.Equal(t, []string{"x", "y"}, cfg.AI.Models.Deny)
}

func TestProjectRules_UnionNullProjectCannotClear(t *testing.T) {
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    deny: [a]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    deny: ~\n")
	cfg, err := loadRules(t, narrow("ai.models.deny", config.Union, nil), "", user, proj)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, cfg.AI.Models.Deny)
}

func TestProjectRules_IntersectNullProjectCannotWiden(t *testing.T) {
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    allow: [a]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: ~\n")
	cfg, err := loadRules(t, narrow("ai.models.allow", config.Intersect, nil), "", user, proj)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, cfg.AI.Models.Allow)
}

func TestProjectRules_IntersectDefaultExact(t *testing.T) {
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    allow: [a, b, c]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: [b, c, d]\n")
	cfg, err := loadRules(t, narrow("ai.models.allow", config.Intersect, nil), "", user, proj)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, cfg.AI.Models.Allow)
}

func TestProjectRules_IntersectNoLowerListProjectStands(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: [a, b]\n")
	cfg, err := loadRules(t, narrow("ai.models.allow", config.Intersect, nil), "", "", proj)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, cfg.AI.Models.Allow)
}

func TestProjectRules_IntersectCustomMatcherNeverWidens(t *testing.T) {
	// lower: glob patterns; project: narrower concrete entries and a wider glob.
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    allow: ['gpt-*', 'claude-opus']\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: ['gpt-4', 'claude-*', 'llama']\n")
	cfg, err := loadRules(t, narrow("ai.models.allow", config.Intersect, globMatch), "", user, proj)
	require.NoError(t, err)
	// gpt-4: admitted by gpt-*; claude-opus: admitted by claude-*;
	// claude-* not admitted by any lower pattern as a value ("claude-*" vs
	// "claude-opus" pattern: no); gpt-* not admitted by project list; llama out.
	assert.ElementsMatch(t, []string{"gpt-4", "claude-opus"}, cfg.AI.Models.Allow)
}

func TestProjectRules_IntersectEmptyIsError(t *testing.T) {
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    allow: [a]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: [b]\n")
	cfg, err := loadRules(t, narrow("ai.models.allow", config.Intersect, nil), "", user, proj)
	require.Error(t, err)
	var ee *config.ErrNarrowEmpty
	require.True(t, errors.As(err, &ee))
	assert.Equal(t, proj, ee.File)
	assert.Equal(t, "ai.models.allow", ee.Key)
	assert.Equal(t, []string{"a"}, ee.Lower)
	assert.Equal(t, []string{"b"}, ee.Project)
	assert.Contains(t, err.Error(), proj)
	assert.Contains(t, err.Error(), "ai.models.allow")
	// Load fails; the project list never reached dst.
	assert.NotContains(t, cfg.AI.Models.Allow, "b")
}

func TestProjectRules_IntersectEmptyProjectListIsError(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: []\n")
	_, err := loadRules(t, narrow("ai.models.allow", config.Intersect, nil), "", "", proj)
	var ee *config.ErrNarrowEmpty
	require.True(t, errors.As(err, &ee))
}

func TestProjectRules_NarrowNonListIsError(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: a\n")
	_, err := loadRules(t, narrow("ai.models.allow", config.Intersect, nil), "", "", proj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ai.models.allow")
	assert.Contains(t, err.Error(), proj)
}

func TestProjectRules_NarrowBadModeIsError(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: [a]\n")
	_, err := loadRules(t, narrow("ai.models.allow", 0, nil), "", "", proj)
	require.Error(t, err)
}

func TestProjectRules_OnNarrowReports(t *testing.T) {
	user := writeRaw(t, "u.yaml", "ai:\n  models:\n    allow: [a, b]\n")
	proj := writeRaw(t, "p.yaml", "ai:\n  models:\n    allow: [b, c]\n")
	var got []config.Narrowed
	rules := narrow("ai.models.allow", config.Intersect, nil)
	rules.OnNarrow = func(n config.Narrowed) { got = append(got, n) }
	_, err := loadRules(t, rules, "", user, proj)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, proj, got[0].File)
	assert.Equal(t, "ai.models.allow", got[0].Key)
	assert.Equal(t, []string{"a", "b"}, got[0].Lower)
	assert.Equal(t, []string{"b", "c"}, got[0].Project)
	assert.Equal(t, []string{"b"}, got[0].Result)
}

func TestProjectRules_ForbiddenWinsBeforeAnyMerge(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "name: p\nbos:\n  url: x\n")
	cfg, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"bos"}}, "", "", proj)
	require.Error(t, err)
	assert.Empty(t, cfg.Name, "rejected project file must not partially merge")
	assert.False(t, strings.Contains(err.Error(), "load config"), err.Error())
}

func TestProjectRules_WithMigrations(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "owner_profile: x\n")
	var cfg rulesCfg
	err := config.Load(&cfg, config.Options{
		ProjectConfigPath: proj,
		Migrations: []config.Migration{{
			From: 0, To: 1,
			Apply: func(m map[string]any) (map[string]any, error) { return m, nil },
		}},
		Project: &config.ProjectRules{Forbidden: []string{"owner_profile"}},
	})
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe))
}

func TestProjectRules_NonStringKeyCannotHideForbidden(t *testing.T) {
	for name, body := range map[string]string{
		"int":     "1: x\nbos:\n  bin: y\n",
		"nested":  "ai:\n  1: x\n  models:\n    deny: [a]\n",
		"bool":    "true: y\n",
		"complex": "? [a]\n: y\n",
		"parent":  "ai:\n  models:\n    allow: [a]\n  2: z\n",
	} {
		t.Run(name, func(t *testing.T) {
			proj := writeRaw(t, "p.yaml", body)
			rules := &config.ProjectRules{
				Forbidden: []string{"bos.bin"},
				Narrow:    []config.NarrowKey{{Key: "ai.models.allow", Mode: config.Intersect}},
			}
			cfg, err := loadRules(t, rules, "", "", proj)
			require.Error(t, err)
			assert.Empty(t, cfg.Bos.URL)
			if name == "complex" {
				// yaml.v3 cannot build this key at all: still fails closed.
				return
			}
			var fe *config.ErrProjectForbidden
			require.True(t, errors.As(err, &fe), "got %v", err)
			assert.Equal(t, proj, fe.File)
			assert.True(t, fe.NonString)
		})
	}
}

func TestProjectRules_NonStringKeyHidingBosBin(t *testing.T) {
	// Non-string sibling key turns the parent into map[any]any in yaml.v3.
	proj := writeRaw(t, "p.yaml", "bos:\n  1: a\n  bin: evil\n")
	_, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"bos.bin"}}, "", "", proj)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe), "got %v", err)
}

func TestProjectRules_SecretUnderSequenceElement(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "servers:\n  - name: a\n    auth:\n      token: abc\n")
	rules := &config.ProjectRules{SecretKey: regexp.MustCompile(`(?i)token`)}
	_, err := loadRules(t, rules, "", "", proj)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe), "got %v", err)
	assert.Equal(t, "servers.auth.token", fe.Key)
}

func TestProjectRules_ForbiddenUnderSequenceElement(t *testing.T) {
	proj := writeRaw(t, "p.yaml", "servers:\n  - bos:\n      url: x\n")
	_, err := loadRules(t, &config.ProjectRules{Forbidden: []string{"servers.bos"}}, "", "", proj)
	var fe *config.ErrProjectForbidden
	require.True(t, errors.As(err, &fe), "got %v", err)
}
