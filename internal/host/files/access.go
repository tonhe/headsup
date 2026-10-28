package files

import (
	"io/fs"
	"slices"
)

// AccessMode is a bit set of the access(2)-style rights to check.
type AccessMode uint8

const (
	AccessRead AccessMode = 1 << iota
	AccessWrite
	AccessExec
)

// Ident is the identity a permission check is evaluated for.
type Ident struct {
	UID  int
	GIDs []int // primary and supplementary groups
}

// Allowed reports whether ident may access a file with the given mode and
// owner, following the classic owner/group/other evaluation of access(2).
// Root may read and write anything and execute anything with an x bit.
// ACLs and capabilities are not considered.
func Allowed(id Ident, mode fs.FileMode, uid, gid int, want AccessMode) bool {
	perm := mode.Perm()
	if id.UID == 0 {
		if want&AccessExec != 0 && !mode.IsDir() && perm&0o111 == 0 {
			return false
		}
		return true
	}
	var bits fs.FileMode
	switch {
	case id.UID == uid:
		bits = (perm >> 6) & 7
	case slices.Contains(id.GIDs, gid):
		bits = (perm >> 3) & 7
	default:
		bits = perm & 7
	}
	if want&AccessRead != 0 && bits&4 == 0 {
		return false
	}
	if want&AccessWrite != 0 && bits&2 == 0 {
		return false
	}
	if want&AccessExec != 0 && bits&1 == 0 {
		return false
	}
	return true
}

// AllowedInfo is Allowed for an fs.FileInfo. When the owner cannot be read
// (non-unix platforms) only the "other" bits are trusted.
func AllowedInfo(id Ident, fi fs.FileInfo, want AccessMode) bool {
	uid, gid, ok := Owner(fi)
	if !ok {
		uid, gid = -1, -1
	}
	return Allowed(id, fi.Mode(), uid, gid, want)
}
