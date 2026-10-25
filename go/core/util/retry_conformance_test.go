package util

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Runs the shared corpus in contracts/retry-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const retryConfDir = "../../../contracts/retry-v1"

// retryConfFiles is every vector file this runner consumes; the corpus
// directory must match it exactly.
var retryConfFiles = map[string]bool{"backoff.json": true}

type retryConfCfg struct {
	BaseNS int64 `json:"base_ns"`
	MaxNS  int64 `json:"max_ns"`
}

func (c retryConfCfg) config(jitter bool) RetryConfig {
	return RetryConfig{
		BaseDelay: time.Duration(c.BaseNS),
		MaxDelay:  time.Duration(c.MaxNS),
		Jitter:    jitter,
	}
}

type retryConfFile struct {
	Schema      string `json:"$schema"`
	ID          string `json:"$id"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Defaults    struct {
		BaseNS int64 `json:"base_ns"`
		MaxNS  int64 `json:"max_ns"`
	} `json:"defaults"`
	Backoff []struct {
		Name    string       `json:"name"`
		Config  retryConfCfg `json:"config"`
		Attempt int          `json:"attempt"`
		DelayNS int64        `json:"delay_ns"`
	} `json:"backoff"`
	JitterBounds []struct {
		Name    string       `json:"name"`
		Config  retryConfCfg `json:"config"`
		Attempt int          `json:"attempt"`
		MaxNS   int64        `json:"max_ns"`
	} `json:"jitter_bounds"`
	MaxAttempts []struct {
		Name        string `json:"name"`
		MaxAttempts int    `json:"max_attempts"`
		Attempt     int    `json:"attempt"`
		Stop        bool   `json:"stop"`
	} `json:"max_attempts"`
}

func loadRetryConf(t *testing.T) retryConfFile {
	t.Helper()
	dir := filepath.FromSlash(retryConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !retryConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "backoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f retryConfFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("parse backoff.json: %v", err)
	}
	if f.Version != "v1" {
		t.Fatalf("version = %q, want v1", f.Version)
	}
	return f
}

func TestConformanceRetryDefaults(t *testing.T) {
	f := loadRetryConf(t)
	c := RetryConfig{}
	if got := int64(c.baseDelay()); got != f.Defaults.BaseNS {
		t.Errorf("base = %d, want %d", got, f.Defaults.BaseNS)
	}
	if got := int64(c.maxDelay()); got != f.Defaults.MaxNS {
		t.Errorf("max = %d, want %d", got, f.Defaults.MaxNS)
	}
}

func TestConformanceRetryBackoff(t *testing.T) {
	for _, c := range loadRetryConf(t).Backoff {
		if got := int64(c.Config.config(false).Backoff(c.Attempt)); got != c.DelayNS {
			t.Errorf("%s: %d, want %d", c.Name, got, c.DelayNS)
		}
	}
}

func TestConformanceRetryJitterBounds(t *testing.T) {
	for _, c := range loadRetryConf(t).JitterBounds {
		cfg := c.Config.config(true)
		if got := int64(unjittered(cfg).Backoff(c.Attempt)); got != c.MaxNS {
			t.Errorf("%s: unjittered %d, want %d", c.Name, got, c.MaxNS)
		}
		for i := 0; i < 2000; i++ {
			if d := int64(cfg.Backoff(c.Attempt)); d < 0 || d > c.MaxNS {
				t.Fatalf("%s: %d outside [0, %d]", c.Name, d, c.MaxNS)
			}
		}
	}
}

// TestConformanceRetryMaxAttempts mirrors the stop rule inside Retry:
// attempt+1 >= MaxAttempts, with 0 unlimited. Driven through Retry itself.
func TestConformanceRetryMaxAttempts(t *testing.T) {
	for _, c := range loadRetryConf(t).MaxAttempts {
		if c.Attempt > 10 {
			continue // Retry would loop; the unlimited case is covered by the Rust runner and TestRetry_*
		}
		calls := 0
		_ = Retry(t.Context(), RetryConfig{MaxAttempts: c.MaxAttempts, BaseDelay: time.Nanosecond, MaxDelay: time.Nanosecond}, func() error {
			calls++
			if calls > c.Attempt+1 {
				return nil
			}
			return errFail
		})
		stopped := calls == c.Attempt+1
		if stopped != c.Stop {
			t.Errorf("%s: stopped=%v, want %v (calls=%d)", c.Name, stopped, c.Stop, calls)
		}
	}
}

var errFail = errors.New("fail")

func unjittered(c RetryConfig) RetryConfig {
	c.Jitter = false
	return c
}
