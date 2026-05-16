package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"charm.land/log/v2"
	"hop.top/kit/go/runtime/bus"
)

// result captures the outcome of a single probe check.
type result struct {
	Target  string
	OK      bool
	Status  int
	Latency time.Duration
	Err     string
}

// runProbe checks all targets and publishes bus events.
func runProbe(cfg *probeConfig, b bus.Bus, logger *log.Logger) []result {
	ctx := context.Background()
	results := make([]result, 0, len(cfg.Targets))
	// Track previous state for recovery detection
	prevFailing := make(map[string]bool)

	for i, t := range cfg.Targets {
		logger.Info("checking",
			"target", t.Name,
			"url", t.URL,
			"progress", fmt.Sprintf("%d/%d", i+1, len(cfg.Targets)),
		)

		r := checkTarget(t)
		results = append(results, r)

		payload := map[string]any{
			"target":  r.Target,
			"ok":      r.OK,
			"status":  r.Status,
			"latency": r.Latency.String(),
			"source":  "probe/go",
			"method":  t.Method,
		}
		if r.Err != "" {
			payload["error"] = r.Err
		}

		_ = b.Publish(ctx, bus.NewEvent("kit.probe.check.executed", "probe/go", payload))

		if !r.OK {
			_ = b.Publish(ctx, bus.NewEvent("kit.probe.check.alerted", "probe/go", payload))
			prevFailing[t.Name] = true
		} else if prevFailing[t.Name] {
			_ = b.Publish(ctx, bus.NewEvent("kit.probe.check.recovered", "probe/go", payload))
			delete(prevFailing, t.Name)
		}
	}

	return results
}

func checkTarget(t targetEntry) result {
	timeout := parseDuration(t.Timeout)

	client := &http.Client{Timeout: timeout}

	req, err := http.NewRequest(t.Method, t.URL, nil)
	if err != nil {
		return result{Target: t.Name, Err: err.Error()}
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)

	if err != nil {
		return result{
			Target:  t.Name,
			Latency: latency,
			Err:     err.Error(),
		}
	}
	defer resp.Body.Close()

	ok := resp.StatusCode == t.Expect.Status

	return result{
		Target:  t.Name,
		OK:      ok,
		Status:  resp.StatusCode,
		Latency: latency,
	}
}

func printSummary(results []result) {
	fmt.Println()
	fmt.Println("=== Probe Summary ===")
	passed, failed := 0, 0
	for _, r := range results {
		status := "PASS"
		if !r.OK {
			status = "FAIL"
			failed++
		} else {
			passed++
		}
		detail := fmt.Sprintf("status=%d latency=%s", r.Status, r.Latency)
		if r.Err != "" {
			detail = fmt.Sprintf("error=%q", r.Err)
		}
		fmt.Printf("  [%s] %-12s %s\n", status, r.Target, detail)
	}
	fmt.Printf("\nTotal: %d | Passed: %d | Failed: %d\n",
		len(results), passed, failed)
}
