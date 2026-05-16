package redact_test

import (
	"strings"
	"testing"

	"hop.top/kit/go/core/redact"
)

// loadDefaultBench loads a fresh Default-policy Redactor (gitleaks +
// Presidio) for benchmarks. We avoid the package singleton so benchmark
// runs are independent.
func loadDefaultBench(b *testing.B) *redact.Redactor {
	b.Helper()
	r := redact.New()
	gl, err := redact.LoadGitleaks(redact.DefaultGitleaksPath())
	if err != nil {
		b.Fatalf("load gitleaks: %v", err)
	}
	r.AddRules(gl...)
	pii, err := redact.LoadPresidio(redact.DefaultPresidioPath())
	if err != nil {
		b.Fatalf("load presidio: %v", err)
	}
	r.AddRules(pii...)
	return r
}

// makePayload returns an n-byte log line built from a repeating phrase.
// Clean = no secrets; dirty = sprinkle n secrets evenly.
func makePayload(n int) string {
	const phrase = "GET /api/v1/users 200 12ms user_id=42 trace=a1b2c3 "
	var sb strings.Builder
	sb.Grow(n)
	for sb.Len() < n {
		sb.WriteString(phrase)
	}
	out := sb.String()
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// makeDirtyPayload sprinkles canonical secrets into a clean payload.
func makeDirtyPayload(size, numSecrets int) string {
	clean := makePayload(size)
	if numSecrets == 0 {
		return clean
	}
	secrets := []string{
		"sk-" + strings.Repeat("a", 20) + "T3BlbkFJ" + strings.Repeat("b", 20),
		"AKIAIOSFODNN7CLIENTX",
		"sk-ant-api03-" + strings.Repeat("a", 93) + "AA",
		"jad@example.com",
		"192.168.1.42",
	}
	stride := len(clean) / (numSecrets + 1)
	var sb strings.Builder
	sb.Grow(len(clean) + numSecrets*48)
	for i := 0; i < numSecrets; i++ {
		off := stride * (i + 1)
		if off >= len(clean) {
			off = len(clean) - 1
		}
		sb.WriteString(clean[:off])
		sb.WriteString(" ")
		sb.WriteString(secrets[i%len(secrets)])
		sb.WriteString(" ")
		clean = clean[off:]
	}
	sb.WriteString(clean)
	return sb.String()
}

func BenchmarkApplyClean(b *testing.B) {
	r := loadDefaultBench(b)
	in := makePayload(4096)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Apply(in)
	}
}

func BenchmarkApplyDirty(b *testing.B) {
	r := loadDefaultBench(b)
	in := makeDirtyPayload(4096, 5)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Apply(in)
	}
}

func BenchmarkApplyLargePayload(b *testing.B) {
	r := loadDefaultBench(b)
	in := makeDirtyPayload(1<<20, 20) // 1 MiB with 20 secrets
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Apply(in)
	}
}

func BenchmarkRuleAdd(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r := redact.New()
		_, err := r.AddRule("openai", `sk-[a-zA-Z0-9]{20,}`, "")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScan(b *testing.B) {
	r := loadDefaultBench(b)
	in := makeDirtyPayload(4096, 5)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = r.Scan(in)
	}
}
