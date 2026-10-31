package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sqlite "modernc.org/sqlite"

	"github.com/tonhe/headsup/internal/host"
	"github.com/tonhe/headsup/internal/host/db"
	"github.com/tonhe/headsup/internal/host/files"
)

// SQLiteOnline copies env.DBPath into dst with SQLite's online backup API,
// which takes a consistent snapshot while headscale keeps running. The
// source is opened read-only (mode=ro, query_only), so it is never written;
// a WAL database in a directory this process cannot write is opened
// immutable, which may miss the newest uncheckpointed changes.
func SQLiteOnline(ctx context.Context, env host.Env, dst string) (string, error) {
	src := env.DBPath
	fi, err := os.Stat(src)
	if err != nil {
		if os.IsPermission(err) {
			return "", &host.ErrPermission{Need: host.NeedDBRead, Op: "read " + src,
				FixCommand: "sudo usermod -aG headscale $USER && newgrp headscale"}
		}
		return "", err
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s is a directory", src)
	}
	immutable := isWAL(src) && !dirWritable(filepath.Dir(src))
	sdb, err := sql.Open("sqlite", db.SQLiteDSN(src, immutable))
	if err != nil {
		return "", err
	}
	defer sdb.Close()
	conn, err := sdb.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	err = conn.Raw(func(dc any) error {
		b, ok := dc.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("sqlite driver lacks the online backup API")
		}
		bk, err := b.NewBackup(dst)
		if err != nil {
			return err
		}
		for {
			if err := ctx.Err(); err != nil {
				_ = bk.Finish()
				return err
			}
			more, err := bk.Step(512)
			if err != nil {
				_ = bk.Finish()
				return err
			}
			if !more {
				return bk.Finish()
			}
		}
	})
	if err != nil {
		if os.IsPermission(err) {
			return "", &host.ErrPermission{Need: host.NeedDBRead, Op: "read " + src,
				FixCommand: "sudo usermod -aG headscale $USER && newgrp headscale"}
		}
		return "", err
	}
	return MethodSQLiteBackupAPI, nil
}

// SimulatedDB stands in for the database of the in-memory demo host.
func SimulatedDB(_ context.Context, env host.Env, dst string) (string, error) {
	return MethodSimulated, os.WriteFile(dst, []byte("simulated headscale database "+env.DBPath+"\n"), 0o600)
}

func isWAL(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	hdr := make([]byte, 20)
	n, _ := f.Read(hdr)
	return n == 20 && string(hdr[:15]) == "SQLite format 3" && (hdr[18] == 2 || hdr[19] == 2)
}

func dirWritable(dir string) bool {
	fi, err := os.Stat(dir)
	if err != nil {
		return false
	}
	g, _ := os.Getgroups()
	id := files.Ident{UID: os.Getuid(), GIDs: append([]int{os.Getgid()}, g...)}
	return files.AllowedInfo(id, fi, files.AccessWrite|files.AccessExec)
}
