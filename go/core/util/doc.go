// Package util provides common utility helpers for kit projects.
//
// Helpers:
//   - env: typed environment variable reader with defaults
//   - fingerprint: consistent short SHA-256 hashes for IDs
//   - humanize: human-friendly durations and byte sizes
//   - jsonl: newline-delimited JSON read/write/stream
//   - must: panic-on-error wrappers for init-time setup
//   - ptr: generic pointer helpers
//   - retry: configurable retry with backoff
//   - since: backward-looking time parsing ("yesterday", "3d ago")
//   - slug: URL-safe slug generation
//   - storage_time: UTC RFC3339 encode/decode + tz-aware same-day predicate
//   - timezone: tz name resolution + display-time formatting
//   - until: forward-looking time parsing ("tomorrow", "in 3d")
package util
