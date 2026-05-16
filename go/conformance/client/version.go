package client

// ClientVersion is the semver string baked into the User-Agent and
// the X-Kit-Client-Version request header. Adopter integrations can
// compare this against the kit binary's reported version to detect
// drift in self-hosted deployments.
const ClientVersion = "0.1.0"

// CassetteSchemaVersion is the manifest.yaml schema version this
// client writes. svc honors requests whose
// X-Kit-Cassette-Schema-Version matches a version it knows; a
// mismatch produces a typed 422.
const CassetteSchemaVersion = "1"

// CassetteMIMEType is the Content-Type used on cassette POSTs. svc
// pins the same string; tests compare against this constant.
const CassetteMIMEType = "application/vnd.kit.cassette+tar+gzip"
