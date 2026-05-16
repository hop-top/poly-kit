package cmdsurface

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		ann  map[string]string
		want SafetyClass
	}{
		{
			name: "nil_annotations",
			ann:  nil,
			want: SafetyClass{},
		},
		{
			name: "read_only",
			ann:  map[string]string{"kit/side-effect": "read"},
			want: SafetyClass{},
		},
		{
			name: "write_is_not_destructive",
			ann:  map[string]string{"kit/side-effect": "write"},
			want: SafetyClass{},
		},
		{
			name: "destructive_legacy",
			ann:  map[string]string{"kit/side-effect": "destructive"},
			want: SafetyClass{Destructive: true},
		},
		{
			name: "destructive_local",
			ann:  map[string]string{"kit/side-effect": "destructive-local"},
			want: SafetyClass{Destructive: true},
		},
		{
			name: "destructive_shared",
			ann:  map[string]string{"kit/side-effect": "destructive-shared"},
			want: SafetyClass{Destructive: true},
		},
		{
			name: "auth_required",
			ann:  map[string]string{"kit/auth-required": "true"},
			want: SafetyClass{AuthRequired: true},
		},
		{
			name: "auth_required_false",
			ann:  map[string]string{"kit/auth-required": "false"},
			want: SafetyClass{},
		},
		{
			name: "requires_confirmation",
			ann:  map[string]string{"kit/requires-confirmation": "true"},
			want: SafetyClass{RequiresConfirmation: true},
		},
		{
			name: "permissions_csv",
			ann:  map[string]string{"kit/permissions": "widget:write, audit:read"},
			want: SafetyClass{Permissions: []string{"widget:write", "audit:read"}},
		},
		{
			name: "exit_codes_csv",
			ann:  map[string]string{"kit/exit-codes": "OK,USAGE,CONFLICT"},
			want: SafetyClass{ExitCodes: []string{"OK", "USAGE", "CONFLICT"}},
		},
		{
			name: "all_combined",
			ann: map[string]string{
				"kit/side-effect":           "destructive",
				"kit/auth-required":         "true",
				"kit/requires-confirmation": "true",
				"kit/permissions":           "p1,p2",
				"kit/exit-codes":            "OK",
			},
			want: SafetyClass{
				Destructive:          true,
				AuthRequired:         true,
				RequiresConfirmation: true,
				Permissions:          []string{"p1", "p2"},
				ExitCodes:            []string{"OK"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "x", Annotations: tc.ann}
			got := Classify(cmd)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%#v want=%#v", got, tc.want)
			}
		})
	}
}

func TestClassify_NilCmd(t *testing.T) {
	got := Classify(nil)
	if !reflect.DeepEqual(got, SafetyClass{}) {
		t.Fatalf("nil cmd should yield zero class, got %#v", got)
	}
}

func TestPolicy_Allowed(t *testing.T) {
	tests := []struct {
		name   string
		policy Policy
		cls    SafetyClass
		s      Surface
		want   bool
	}{
		// Local surfaces always allowed.
		{"cli_read", DefaultPolicy(), SafetyClass{}, SurfaceCLI, true},
		{"cli_destructive", DefaultPolicy(), SafetyClass{Destructive: true}, SurfaceCLI, true},
		{"lib_read", DefaultPolicy(), SafetyClass{}, SurfaceLib, true},
		{"lib_destructive", DefaultPolicy(), SafetyClass{Destructive: true}, SurfaceLib, true},
		// Non-destructive always allowed on any surface.
		{"rest_read", DefaultPolicy(), SafetyClass{}, SurfaceREST, true},
		{"webhook_read", DefaultPolicy(), SafetyClass{}, SurfaceWebhook, true},
		// Destructive blocked on remote surfaces by default.
		{"rest_destructive_default", DefaultPolicy(), SafetyClass{Destructive: true}, SurfaceREST, false},
		{"ws_destructive_default", DefaultPolicy(), SafetyClass{Destructive: true}, SurfaceWS, false},
		{"webhook_destructive_default", DefaultPolicy(), SafetyClass{Destructive: true}, SurfaceWebhook, false},
		// Opt-in via AllowDestructiveOn.
		{
			"rest_destructive_optin",
			Policy{AllowDestructiveOn: []Surface{SurfaceREST}},
			SafetyClass{Destructive: true},
			SurfaceREST,
			true,
		},
		// Opt-in for one surface does not leak to another.
		{
			"ws_destructive_no_optin",
			Policy{AllowDestructiveOn: []Surface{SurfaceREST}},
			SafetyClass{Destructive: true},
			SurfaceWS,
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.policy.Allowed(tc.cls, tc.s)
			if got != tc.want {
				t.Fatalf("Allowed(%v,%v)=%v want=%v", tc.cls, tc.s, got, tc.want)
			}
		})
	}
}

func TestPolicy_DefaultEnabled_Fallback(t *testing.T) {
	p := Policy{} // zero value
	defs := p.resolvedDefaults()
	want := []Surface{SurfaceCLI, SurfaceLib, SurfaceMCP}
	if !reflect.DeepEqual(defs, want) {
		t.Fatalf("zero Policy defaults=%v want=%v", defs, want)
	}
	p2 := Policy{DefaultEnabled: []Surface{SurfaceCLI}}
	if !reflect.DeepEqual(p2.resolvedDefaults(), []Surface{SurfaceCLI}) {
		t.Fatalf("explicit DefaultEnabled must take precedence")
	}
}
