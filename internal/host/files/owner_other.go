//go:build !unix

package files

import "io/fs"

// Owner is unavailable on this platform.
func Owner(fi fs.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }

func currentUID() int { return -1 }

func currentGIDs() []int { return nil }
