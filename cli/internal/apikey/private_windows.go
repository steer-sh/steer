package apikey

import "io/fs"

// checkPrivate accepts every path: Windows reports no owner or permission bits
// through fs.FileInfo, so nothing here checks who can read the key. No Windows
// binary ships.
func checkPrivate(string, fs.FileInfo, fs.FileMode) error { return nil }
