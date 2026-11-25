package cc

// searchable reports whether this process may enter the directory; Windows has no
// search permission bit.
func searchable(string) bool { return true }
