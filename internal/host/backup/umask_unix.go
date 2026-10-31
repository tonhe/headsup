//go:build unix

package backup

import (
	"sync"
	"syscall"
)

var umaskMu sync.Mutex

// privateUmask sets a 0077 umask so nothing the bundle creates (including
// SQLite's temporary journal next to the copy) is readable by others, and
// returns a func restoring the previous one. Explicit 0700/0600 modes are
// still set on every path, so this is a second line of defence.
func privateUmask() func() {
	umaskMu.Lock()
	old := syscall.Umask(0o077)
	return func() {
		syscall.Umask(old)
		umaskMu.Unlock()
	}
}
