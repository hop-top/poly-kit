package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var backtickRe = regexp.MustCompile("`([^`]+)`")

// loadTiers maps each SDK directory named in the tiers page's "Today"
// column to its lower-cased tier name.
func loadTiers(file, display string) (map[string]string, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	found := false
	for _, t := range scanTables(string(b)) {
		if len(t.Header) != 3 || t.Header[0] != "Tier" || t.Header[2] != "Today" {
			continue
		}
		found = true
		for _, r := range t.Rows {
			if len(r.Cells) != 3 {
				return nil, fmt.Errorf("%s:%d: tier row has %d columns, want 3", display, r.Line, len(r.Cells))
			}
			tier := strings.ToLower(strings.Trim(r.Cells[0], "*` "))
			for _, m := range backtickRe.FindAllStringSubmatch(r.Cells[2], -1) {
				if prev, dup := out[m[1]]; dup && prev != tier {
					return nil, fmt.Errorf("%s:%d: %s is in tiers %q and %q", display, r.Line, m[1], prev, tier)
				}
				out[m[1]] = tier
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%s: no tiers table (header: Tier | Meaning | Today)", display)
	}
	return out, nil
}
