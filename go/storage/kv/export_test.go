package kv

// UnregisterBackend removes a backend from both registries.
//
// Test-only, and deliberately not part of the public API: the registries
// are process-wide and populated from driver init functions, so a
// production unregister would be a way to break a program at a distance.
// Tests that register a stub backend need it so the exact-set assertion in
// TestBackendsMatchesDocumentedSet keeps guarding doc drift.
func UnregisterBackend(name string) {
	delete(backends, name)
	delete(ctxBackends, name)
}
