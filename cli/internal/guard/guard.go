// Package guard holds the master-hook runtime: the Python that normalizes a
// harness payload into per-file virtual edits, runs every installed guard over
// each, and answers in the harness's native deny contract.
//
// The runtime is spliced into the generated master hook rather than shipped as
// its own file, because a guard installation is one self-contained script.
package guard

import (
	_ "embed"
	"strings"
)

// HarnessFlag is the master hook's argument naming the harness it answers to.
// Claude Code is the default, so a Claude registration carries no flag and every
// settings.json written before this runtime existed keeps working.
const HarnessFlag = "--harness"

//go:embed runtime.py
var runtimeSource string

// RuntimeSource is the Python the generated master embeds. It declares no
// imports: ast, datetime, hashlib, io, json, os, stat and sys come from the master's
// import block.
func RuntimeSource() string {
	return strings.TrimRight(runtimeSource, "\n") + "\n"
}

// RequiredImports are the modules RuntimeSource assumes are already imported.
func RequiredImports() []string {
	return []string{"import ast", "import datetime", "import hashlib", "import io", "import json", "import os", "import stat", "import sys"}
}
