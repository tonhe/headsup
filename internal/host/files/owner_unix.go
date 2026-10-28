//go:build unix

package files

import (
	"io/fs"
	"os"
	"syscall"
)

// Owner returns the uid and gid recorded in fi, when the platform exposes them.
func Owner(fi fs.FileInfo) (uid, gid int, ok bool) {
	if fi == nil {
		return 0, 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}

func currentUID() int { return os.Getuid() }

func currentGIDs() []int {
	g, _ := os.Getgroups()
	return append([]int{os.Getgid()}, g...)
}
