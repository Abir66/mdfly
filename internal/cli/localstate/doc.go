// Package localstate is the durable, slug-keyed publish ledger under the CLI
// state directory (ADR-0009). It owns state.json (the ledger), the separate
// credentials file (Edit Tokens, per ADR-0008 — state holds only a has_token
// marker), and cache.json (the volatile update check). Writes are atomic
// (temp+rename) and serialized by an advisory flock; a corrupt state.json is
// backed up and replaced with an empty ledger so publish always works.
package localstate
