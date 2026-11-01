package gate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ScanFunc receives one path's content. data is nil when the file is larger than the
// scan's limit, with size its length, or when it could not be read, with size -1. A
// false return stops the scan.
type ScanFunc func(path string, data []byte, size int64) bool

// Scan reads paths in order and calls fn with each, skipping any path the view does
// not hold. Index objects come through one `git cat-file --batch-command` process, so
// a scan over many files starts one git, not one per file, and a blob over maxSize is
// sized without being read.
func (v *CommitView) Scan(ctx context.Context, paths []string, maxSize int64, fn ScanFunc) error {
	var batch *catFileBatch
	defer func() {
		if batch != nil {
			batch.close()
		}
	}()
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		object, ok := v.blobs[path]
		if !ok {
			continue
		}
		var data []byte
		size := int64(-1)
		if object == "" {
			data, size = v.scanDisk(path, maxSize)
		} else {
			if batch == nil {
				b, err := startCatFileBatch(ctx, v.root)
				if err != nil {
					return err
				}
				batch = b
			}
			var err error
			data, size, err = batch.read(object, maxSize)
			if err != nil {
				return err
			}
		}
		if !fn(path, data, size) {
			return nil
		}
	}
	return nil
}

// scanDisk reads a working-tree file within maxSize.
func (v *CommitView) scanDisk(path string, maxSize int64) ([]byte, int64) {
	root, err := os.OpenRoot(v.root)
	if err != nil {
		return nil, -1
	}
	defer root.Close()
	if regularAt(root, path) != nil {
		return nil, -1
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, -1
	}
	if info.Size() > maxSize {
		return nil, info.Size()
	}
	data, err := v.readDisk(path, maxSize)
	if err != nil {
		return nil, -1
	}
	if int64(len(data)) > maxSize {
		return nil, max(info.Size(), int64(len(data)))
	}
	return data, int64(len(data))
}

// catFileBatch is one unbuffered `git cat-file --batch-command` process, which
// answers each command before it reads the next.
type catFileBatch struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
}

func startCatFileBatch(ctx context.Context, root string) (*catFileBatch, error) {
	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch-command")
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &catFileBatch{cmd: cmd, stdin: stdin, out: bufio.NewReader(stdout)}, nil
}

// read returns object's content, or nil with its size when it exceeds maxSize, or
// nil with -1 when git does not hold it.
func (b *catFileBatch) read(object string, maxSize int64) ([]byte, int64, error) {
	size, err := b.header("info " + object)
	if err != nil || size < 0 || size > maxSize {
		return nil, size, err
	}
	if size, err = b.header("contents " + object); err != nil || size < 0 {
		return nil, -1, err
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(b.out, data); err != nil {
		return nil, -1, err
	}
	// Each object ends with a newline the content does not hold.
	if _, err := b.out.ReadByte(); err != nil {
		return nil, -1, err
	}
	return data, size, nil
}

// header sends one command and reads its "<object> <type> <size>" answer; -1 for
// "<object> missing".
func (b *catFileBatch) header(command string) (int64, error) {
	if _, err := io.WriteString(b.stdin, command+"\n"); err != nil {
		return -1, err
	}
	line, err := b.out.ReadString('\n')
	if err != nil {
		return -1, err
	}
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return -1, nil
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return -1, fmt.Errorf("cat-file header %q", strings.TrimSpace(line))
	}
	return size, nil
}

func (b *catFileBatch) close() {
	b.stdin.Close()
	_ = b.cmd.Wait()
}
