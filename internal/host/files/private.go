package files

// Exported forms of the private-directory helpers, so other host packages
// (the backup bundle) apply exactly the same FR-064 rules as per-write
// backups instead of re-implementing them.

// CheckPrivateDir refuses dir with *host.ErrInsecureBackupDir when it is a
// symlink, not a directory, grants any group or other permission, or is
// owned by a uid other than uid (-1 skips the owner check). With create, a
// missing dir is created with mode 0700 first.
func CheckPrivateDir(dir string, uid int, create bool) error {
	return checkPrivateDir(dir, uid, create)
}

// MkdirPrivate creates dir and any missing parents with mode 0700.
func MkdirPrivate(dir string) error { return mkdirPrivate(dir) }

// WritePrivate creates path (which must not exist) with mode 0600, writes
// data and fsyncs it.
func WritePrivate(path string, data []byte) error { return writePrivate(path, data) }

// CurrentUID is this process's uid, or -1 where the platform has none.
func CurrentUID() int { return currentUID() }
