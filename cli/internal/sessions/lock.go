package sessions

import (
	"fmt"
	"os"
)

// lockShard serializes a host's complete refresh. Keep the file after release,
// so every writer locks the same inode; the OS releases it when a process exits.
func lockShard(dir, host string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(ShardPath(dir, host)+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockShardFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock session shard: %w", err)
	}
	return func() {
		unlockShardFile(f)
		f.Close()
	}, nil
}
