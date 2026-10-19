package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToFileWritesStdoutToANewFileAndPrintsItsPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	before := os.Stdout

	var paths []string
	for _, text := range []string{"first", "second"} {
		var out bytes.Buffer
		if err := startDump(&out); err != nil {
			t.Fatal(err)
		}
		fmt.Println(text)
		// The path is printed before the command runs, so an exit inside it loses nothing.
		path := strings.TrimSpace(out.String())
		data, err := os.ReadFile(path)
		if err != nil || string(data) != text+"\n" {
			t.Fatalf("dump holds %q, %v before finish; want %q", data, err, text)
		}
		finishDump()
		if os.Stdout != before {
			t.Fatal("stdout was not restored")
		}
		if filepath.Dir(path) != dir {
			t.Fatalf("dump %q is not under TMPDIR %q", path, dir)
		}
		paths = append(paths, path)
	}
	if paths[0] == paths[1] {
		t.Fatalf("two dumps shared the path %s", paths[0])
	}
	finishDump()
	if os.Stdout != before {
		t.Fatal("finishDump with no dump changed stdout")
	}
}

func TestToFileRefusesWhenNoFileCanBeMade(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "absent"))
	before := os.Stdout
	var out bytes.Buffer
	if err := startDump(&out); err == nil {
		finishDump()
		t.Fatal("startDump made a dump in a directory that does not exist")
	}
	if out.Len() != 0 || os.Stdout != before {
		t.Fatalf("a failed dump printed %q or moved stdout", out.String())
	}
}
