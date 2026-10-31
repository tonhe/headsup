//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

func lookPath(name string) (string, error) { return exec.LookPath(name) }

func execve(path string, argv, env []string) error { return syscall.Exec(path, argv, env) }
