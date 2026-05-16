// White-box tests for evalConditional grammar:
//
//	expr   = or
//	or     = and ( "||" and )*
//	and    = clause ( "&&" clause )*
//	clause = [ "!" ] key "=" value
//
// Verifies simple key=value parity with v1, negation, conjunction,
// disjunction, "&&"-over-"||" precedence, missing-var semantics, and
// malformed-input error paths.
package template

import (
	"strings"
	"testing"
)

func TestEvalConditional(t *testing.T) {
	vars := map[string]any{
		"a":    "1",
		"b":    "2",
		"c":    "3",
		"name": "foo",
		"n":    42,
	}

	cases := []struct {
		name    string
		expr    string
		want    bool
		wantErr string // substring; empty = no error expected
	}{
		// --- v1 parity: simple key=value ---
		{name: "simple match", expr: "a=1", want: true},
		{name: "simple mismatch", expr: "a=2", want: false},
		{name: "missing var", expr: "missing=1", want: false},
		{name: "string value match", expr: "name=foo", want: true},
		{name: "non-string stringifies", expr: "n=42", want: true},
		{name: "non-string mismatch", expr: "n=43", want: false},

		// --- negation ---
		{name: "negate match -> false", expr: "!a=1", want: false},
		{name: "negate mismatch -> true", expr: "!a=2", want: true},
		{name: "negate missing -> true", expr: "!missing=1", want: true},

		// --- disjunction ---
		{name: "or both true", expr: "a=1 || b=2", want: true},
		{name: "or first true", expr: "a=1 || b=99", want: true},
		{name: "or second true", expr: "a=99 || b=2", want: true},
		{name: "or both false", expr: "a=99 || b=99", want: false},

		// --- conjunction ---
		{name: "and both true", expr: "a=1 && b=2", want: true},
		{name: "and first false", expr: "a=99 && b=2", want: false},
		{name: "and second false", expr: "a=1 && b=99", want: false},

		// --- precedence: && tighter than || ---
		// a=1 || (!b=2 && c=3) => true (LHS true short-circuits)
		{name: "mixed precedence LHS true", expr: "a=1 || !b=2 && c=3", want: true},
		// a=99 || (!b=99 && c=3) => false || (true && true) => true
		{name: "mixed precedence RHS true", expr: "a=99 || !b=99 && c=3", want: true},
		// a=99 || (!b=2 && c=3) => false || (false && true) => false
		{name: "mixed precedence RHS false via neg", expr: "a=99 || !b=2 && c=3", want: false},
		// (a=1 && b=99) || c=3 => false || true => true
		{name: "mixed precedence trailing or", expr: "a=1 && b=99 || c=3", want: true},

		// --- whitespace tolerance ---
		{name: "spaces around operators", expr: "  a=1   &&   b=2  ", want: true},
		{name: "spaces around bang", expr: "  !  a=2 ", want: true},

		// --- malformed inputs ---
		{name: "missing equals", expr: "abc", wantErr: "must be key=value"},
		{name: "empty expression", expr: "", wantErr: "empty clause"},
		{name: "empty or clause", expr: "a=1 || ", wantErr: "empty clause"},
		{name: "empty and clause", expr: "a=1 &&  ", wantErr: "empty clause"},
		{name: "lonely bang", expr: "!", wantErr: "empty clause after negation"},
		{name: "bang without equals", expr: "!abc", wantErr: "must be key=value"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalConditional(tc.expr, vars)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (result=%v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("evalConditional(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}
