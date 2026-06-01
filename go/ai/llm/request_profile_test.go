package llm_test

import (
	"strings"
	"testing"

	"hop.top/kit/go/ai/llm"
)

func TestRequestProfile_ZeroValue(t *testing.T) {
	var p llm.RequestProfile

	if p.Filter.ToolCall != nil {
		t.Fatalf("zero Filter.ToolCall: got %v, want nil", p.Filter.ToolCall)
	}
	if p.Filter.Reasoning != nil {
		t.Fatalf("zero Filter.Reasoning: got %v, want nil", p.Filter.Reasoning)
	}
	if p.Filter.OpenWeights != nil {
		t.Fatalf("zero Filter.OpenWeights: got %v, want nil", p.Filter.OpenWeights)
	}
	if p.Filter.StructuredOutput != nil {
		t.Fatalf("zero Filter.StructuredOutput: got %v, want nil", p.Filter.StructuredOutput)
	}
	if p.Filter.Temperature != nil {
		t.Fatalf("zero Filter.Temperature: got %v, want nil", p.Filter.Temperature)
	}
	if p.Filter.Provider != "" || p.Filter.Family != "" || p.Filter.Query != "" {
		t.Fatalf("zero Filter strings: got provider=%q family=%q query=%q, want all empty",
			p.Filter.Provider, p.Filter.Family, p.Filter.Query)
	}
	if len(p.Filter.Input) != 0 || len(p.Filter.Output) != 0 {
		t.Fatalf("zero Filter modalities: got input=%v output=%v, want empty", p.Filter.Input, p.Filter.Output)
	}
	if p.MaxInputTokens != 0 {
		t.Fatalf("zero MaxInputTokens: got %d, want 0", p.MaxInputTokens)
	}
	if p.MaxOutputTokens != 0 {
		t.Fatalf("zero MaxOutputTokens: got %d, want 0", p.MaxOutputTokens)
	}
}

func TestBudgetTier_String(t *testing.T) {
	tests := []struct {
		name string
		tier llm.BudgetTier
		want string
	}{
		{"cheap", llm.BudgetCheap, "cheap"},
		{"balanced", llm.BudgetBalanced, "balanced"},
		{"premium", llm.BudgetPremium, "premium"},
		{"out-of-range", llm.BudgetTier(99), "unknown"},
		{"negative", llm.BudgetTier(-1), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tier.String(); got != tt.want {
				t.Fatalf("BudgetTier(%d).String() = %q, want %q", tt.tier, got, tt.want)
			}
		})
	}
}

func TestParseBudgetTier(t *testing.T) {
	valid := []struct {
		in   string
		want llm.BudgetTier
	}{
		{"cheap", llm.BudgetCheap},
		{"balanced", llm.BudgetBalanced},
		{"premium", llm.BudgetPremium},
		{"Cheap", llm.BudgetCheap},
		{"Balanced", llm.BudgetBalanced},
		{"PREMIUM", llm.BudgetPremium},
		{"  cheap  ", llm.BudgetCheap},
		{"\tbalanced\n", llm.BudgetBalanced},
	}
	for _, tt := range valid {
		t.Run("valid/"+tt.in, func(t *testing.T) {
			got, err := llm.ParseBudgetTier(tt.in)
			if err != nil {
				t.Fatalf("ParseBudgetTier(%q): unexpected error %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseBudgetTier(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}

	invalid := []string{"", "super-cheap", "123", "unknown", "cheaper"}
	for _, in := range invalid {
		t.Run("invalid/"+in, func(t *testing.T) {
			_, err := llm.ParseBudgetTier(in)
			if err == nil {
				t.Fatalf("ParseBudgetTier(%q): want error, got nil", in)
			}
			// The error must echo the original input so operators can surface it.
			if !strings.Contains(err.Error(), in) {
				t.Fatalf("ParseBudgetTier(%q) error %q must contain input", in, err.Error())
			}
		})
	}
}

func TestParseBudgetTier_RoundTrip(t *testing.T) {
	for _, tier := range []llm.BudgetTier{llm.BudgetCheap, llm.BudgetBalanced, llm.BudgetPremium} {
		t.Run(tier.String(), func(t *testing.T) {
			got, err := llm.ParseBudgetTier(tier.String())
			if err != nil {
				t.Fatalf("ParseBudgetTier(%q): %v", tier.String(), err)
			}
			if got != tier {
				t.Fatalf("round trip: ParseBudgetTier(%q) = %v, want %v", tier.String(), got, tier)
			}
		})
	}
}
