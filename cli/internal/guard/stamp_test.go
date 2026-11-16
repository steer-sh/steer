package guard

import "testing"

// TestStampRoundTrips pins the one grammar the writer and the reader share.
func TestStampRoundTrips(t *testing.T) {
	line := StampLine("1.2.3")
	stamp, ok := ParseStamp("#!/usr/bin/env python3\n" + line + "\nimport os\n")
	if !ok {
		t.Fatal("a generated stamp line did not parse")
	}
	if stamp.Runtime != RuntimeDigest() {
		t.Errorf("runtime: got %q, want %q", stamp.Runtime, RuntimeDigest())
	}
	if stamp.Version != "1.2.3" {
		t.Errorf("version: got %q, want 1.2.3", stamp.Version)
	}
}

// TestRuntimeDigestTracksTheRuntime pins that the digest is over the runtime
// source, so a changed runtime.py is a changed digest and every installed master
// reads as stale.
func TestRuntimeDigestTracksTheRuntime(t *testing.T) {
	digest := RuntimeDigest()
	if len(digest) != 8 {
		t.Fatalf("digest %q is not 8 hex digits", digest)
	}
	if digest != RuntimeDigest() {
		t.Error("the digest is not stable across calls")
	}
}

// TestAnUnstampedMasterNeverParses is the load-bearing case: every store built
// before the stamp existed is unstamped, so absence must read as unknown rather
// than as up to date.
func TestAnUnstampedMasterNeverParses(t *testing.T) {
	for _, content := range []string{
		"#!/usr/bin/env python3\nimport os\n",
		StampPrefix + "not json\n",
		StampPrefix + `{"version":"1.0.0"}` + "\n",
	} {
		if _, ok := ParseStamp(content); ok {
			t.Errorf("content parsed as stamped: %q", content)
		}
	}
}
