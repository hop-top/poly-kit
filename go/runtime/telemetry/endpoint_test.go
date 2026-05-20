package telemetry

import "testing"

// TestResolveEndpoint_PrecedenceChain exercises the documented
// resolution order: env > wire > DefaultEndpoint > "".
//
// The test mutates DefaultEndpoint directly because that's the
// production wire (the ldflag target is a package-level var). Each
// sub-test restores the prior value so cases don't leak.
func TestResolveEndpoint_PrecedenceChain(t *testing.T) {
	priorDefault := DefaultEndpoint
	t.Cleanup(func() { DefaultEndpoint = priorDefault })

	cases := []struct {
		name        string
		envOverride string
		wireConfig  string
		ldflag      string
		want        string
	}{
		{
			name: "all empty -> empty",
			want: "",
		},
		{
			name:   "ldflag only",
			ldflag: "https://baked.example/v1",
			want:   "https://baked.example/v1",
		},
		{
			name:       "wire beats ldflag",
			wireConfig: "https://wire.example/v1",
			ldflag:     "https://baked.example/v1",
			want:       "https://wire.example/v1",
		},
		{
			name:        "env beats wire and ldflag",
			envOverride: "https://env.example/v1",
			wireConfig:  "https://wire.example/v1",
			ldflag:      "https://baked.example/v1",
			want:        "https://env.example/v1",
		},
		{
			name:        "env beats empty defaults",
			envOverride: "https://env.example/v1",
			want:        "https://env.example/v1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			DefaultEndpoint = tc.ldflag
			got := ResolveEndpoint(tc.envOverride, tc.wireConfig)
			if got != tc.want {
				t.Fatalf("ResolveEndpoint(%q, %q) with DefaultEndpoint=%q = %q; want %q",
					tc.envOverride, tc.wireConfig, tc.ldflag, got, tc.want)
			}
		})
	}
}
