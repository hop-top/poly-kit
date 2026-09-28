package security_test

// Gap tests for `hop.top/kit/go/security`. Same convention as
// go/console/cli/gaps_test.go — Skip + pin until the gap is closed.
// One test per family named in doc.go.

import "testing"

// Gap: kit/security has no artifact verification.
//
// The upgrade module downloads a release binary and installs it
// without checking a signature. The first family verifies the
// artifact before install: cosign keyless (sigstore bundle, Rekor
// inclusion), minisign (simple key), and SLSA provenance attestation
// parsing for the source repo and builder check.
//
// Desired API (sketch):
//
//	res, err := security.Verify(ctx, artifact, bundle, security.Policy{...})
//	// res.OK is false with res.Reasons populated on any failure;
//	// upgrade refuses to install unless verification passes.
func TestGap_SecurityArtifactVerify_Missing(t *testing.T) {
	t.Skip("gap: kit/security artifact verification not implemented; upgrade installs unsigned downloads")

	// Pin: placeholder package exists; Verify does not. When it
	// ships, this test should verify committed testdata fixtures
	// (a signed artifact plus its public material, no network) for
	// each of cosign, minisign and SLSA, and assert a tampered
	// artifact yields a Result with the reason named.
}

// Gap: kit/security has no sandboxed Exec adapter.
//
// runtime/sideeffect defines the Exec seam but every implementation
// runs the command with the caller's full privileges. Code the tool
// does not trust (adopter plugins, agent-suggested commands) needs a
// platform sandbox: bwrap or Landlock+seccomp on Linux, sandbox-exec
// on macOS, with a degrade-or-refuse policy where none is available.
//
// Desired API (sketch):
//
//	exec := security.SandboxedExec(inner, security.SandboxOptions{Mode: security.Strict})
//	// exec satisfies sideeffect.Exec; Strict refuses when no sandbox
//	// is available, the default mode warns.
func TestGap_SecuritySandboxedExec_Missing(t *testing.T) {
	t.Skip("gap: kit/security sandboxed Exec adapter not implemented; sideeffect.Exec runs untrusted commands unconfined")

	// Pin: placeholder package exists; the adapter does not. When it
	// ships, this test should probe the platform sandbox (skip, not
	// fail, where the binary is absent), run a command that tries to
	// write outside its allowed paths, and assert the write is denied
	// while an allowed one succeeds.
}

// Gap: kit/security's audit log heads are not signed.
//
// AuditLog chains every record to the one before it, so VerifyAuditLog
// catches an edit, a deletion, or a reorder anywhere in the log. It
// cannot catch records removed from the tail: the shorter chain still
// holds. Today an operator closes that gap by recording AuditLog.Head
// somewhere the writer cannot reach. The remaining piece signs the head
// periodically with the identity key and appends the signature as a
// record, so a verifier holding the public key detects truncation on
// its own; the log also does not yet feed runtime/provenance.
//
// Desired API (sketch):
//
//	log, err := security.OpenAuditLog(path, security.AuditLogOptions{Signer: key, SignEvery: time.Minute})
//	report, err := security.VerifyAuditLog(path, security.WithPublicKey(pub))
//	// report.Break names the tail truncation past the last signature
func TestGap_SecurityAuditLogSignedHead_Missing(t *testing.T) {
	t.Skip("gap: kit/security audit log heads are not signed; tail truncation is detectable only against a head recorded elsewhere")

	// Pin: the chain and its verifier exist; signing does not. When it
	// ships, this test should append records past a signed head,
	// truncate the tail, and assert verification with the public key
	// reports the truncation.
}

// Gap: kit/security has no SARIF normalization.
//
// gosec, semgrep, trivy, grype, govulncheck and osv-scanner all emit
// SARIF, and a kit CLI that runs one of them on its own tree has no
// way to print the findings through `--format`. The fourth family
// parses SARIF into kit's output rows (tool, rule, level, location,
// message) so a conformance gate can threshold on level. Invoking
// the scanners stays with the adopter or CI.
//
// Desired API (sketch):
//
//	findings, err := security.ParseSARIF(r)
//	// findings render through output.Table / output.JSON unchanged.
func TestGap_SecuritySARIFNormalize_Missing(t *testing.T) {
	t.Skip("gap: kit/security SARIF normalization not implemented; scanner findings cannot render through the output layer")

	// Pin: placeholder package exists; ParseSARIF does not. When it
	// ships, this test should parse committed SARIF fixtures from at
	// least two scanners and assert the same row shape, then assert a
	// level threshold selects the expected subset.
}
