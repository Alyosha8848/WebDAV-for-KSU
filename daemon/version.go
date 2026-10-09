package main

// Version metadata for the DufsBox control daemon.
//
// Protocol is the version of the App <-> daemon contract documented in
// docs/API.md. Bump it whenever a field changes meaning.

const (
	// DaemonVersion is the DufsBox release version.
	DaemonVersion = "1.0.0"
	// ProtocolVersion is the App <-> daemon contract revision.
	ProtocolVersion = 1
)

// Pinned upstream component versions. These are the versions the module ships
// (see module/checksums.sha256); the real values are also probed from the
// binaries at runtime so a swapped binary cannot silently lie in the UI.
const (
	PinnedDufsVersion      = "0.46.0"
	PinnedTailscaleVersion = "1.102.2"
)
