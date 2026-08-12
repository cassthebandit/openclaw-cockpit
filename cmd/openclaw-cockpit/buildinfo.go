// File buildinfo.go exposes the binary's build identity for operators. The
// identity source is Go's embedded build metadata (vcs.* settings) — the
// single stamping authority — never a second hand-maintained commit stamp
// that could disagree with it.
package main

import (
	"fmt"
	"runtime/debug"
)

// buildIdentity is the machine-readable identity emitted by --build-info.
type buildIdentity struct {
	Product     string `json:"product"`
	Version     string `json:"version"`
	VCSRevision string `json:"vcsRevision,omitempty"`
	// VCSModified is nil when the build carries no VCS evidence, so "unknown"
	// is never conflated with "clean".
	VCSModified *bool  `json:"vcsModified,omitempty"`
	VCSTime     string `json:"vcsTime,omitempty"`
	// IdentitySource is "go-build-metadata" when VCS evidence is embedded and
	// "unavailable" otherwise.
	IdentitySource string `json:"identitySource"`
}

func readBuildIdentity() buildIdentity {
	identity := buildIdentity{
		Product:        productName,
		Version:        version,
		IdentitySource: "unavailable",
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return identity
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			identity.VCSRevision = setting.Value
		case "vcs.modified":
			modified := setting.Value == "true"
			identity.VCSModified = &modified
		case "vcs.time":
			identity.VCSTime = setting.Value
		}
	}
	if identity.VCSRevision != "" {
		identity.IdentitySource = "go-build-metadata"
	}
	return identity
}

// human renders the --version line: short revision plus dirty/clean when the
// embedded evidence exists, an explicit "revision unknown" when it does not.
func (b buildIdentity) human() string {
	if b.VCSRevision == "" {
		return fmt.Sprintf("%s %s (revision unknown)", b.Product, b.Version)
	}
	revision := b.VCSRevision
	if len(revision) > 12 {
		revision = revision[:12]
	}
	state := "vcs state unknown"
	if b.VCSModified != nil {
		if *b.VCSModified {
			state = "dirty"
		} else {
			state = "clean"
		}
	}
	return fmt.Sprintf("%s %s (rev %s, %s)", b.Product, b.Version, revision, state)
}
