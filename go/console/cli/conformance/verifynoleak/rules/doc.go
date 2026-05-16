// Package rules holds the canonical scenario-shape rule set + the
// pure matchers that decide whether a parsed YAML doc is
// scenario-shaped. The data is loaded from
// scenario_rules_embedded.json — a build-time vendored copy of
// contracts/scenario-rules.json.
//
// To refresh the embedded copy after editing the canonical file:
//
//	cp contracts/scenario-rules.json \
//	  go/console/cli/conformance/verifynoleak/rules/scenario_rules_embedded.json
//
// A drift test (rules_drift_test.go) compares the two files at test
// time so out-of-sync state is caught before merge.
package rules
