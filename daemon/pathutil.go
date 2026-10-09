package main

import (
	"path"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// Path handling
//
// Every path DufsBox deals with is an Android path, so POSIX rules must apply
// even when the daemon is compiled for a development host to run the integration
// tests. Using path/filepath directly would rewrite /sdcard into \sdcard on
// Windows, which silently changes the dufs argument vector and would make the
// browse API reject perfectly valid shares.
//
// The Windows branch exists only so the browse API can be unit tested against a
// native temporary directory; on Android every path takes the POSIX branch.
// ---------------------------------------------------------------------------

// isPosixPath reports whether p should be treated with POSIX semantics.
func isPosixPath(p string) bool {
	if p == "" {
		return true
	}
	return strings.HasPrefix(p, "/")
}

// cleanPath normalises a path without changing its flavour.
func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	if isPosixPath(p) {
		return path.Clean(p)
	}
	return filepath.Clean(p)
}

// isAbsPath reports whether p is absolute in either flavour.
func isAbsPath(p string) bool {
	return path.IsAbs(p) || filepath.IsAbs(p)
}

// dirPath returns the parent directory.
func dirPath(p string) string {
	if isPosixPath(p) {
		return path.Dir(p)
	}
	return filepath.Dir(p)
}

// joinPath joins a directory and a child name.
func joinPath(dir, name string) string {
	if isPosixPath(dir) {
		return path.Join(dir, name)
	}
	return filepath.Join(dir, name)
}

// stripBOM removes a UTF-8 byte order mark.
//
// JSON parsers reject a leading BOM, and several editors and shells (notably
// Windows PowerShell's `Set-Content -Encoding UTF8`) add one when writing a
// config file. Treating the BOM as cosmetic keeps a hand-edited config from
// silently resetting the operator's share path and credentials.
func stripBOM(raw []byte) []byte {
	if len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF {
		return raw[3:]
	}
	return raw
}
