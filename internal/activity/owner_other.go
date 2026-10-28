//go:build !unix

package activity

import "io/fs"

func ownedByMe(fs.FileInfo) bool { return true }
