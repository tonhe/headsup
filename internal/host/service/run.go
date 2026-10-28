// Package service controls the headscale service through the detected
// service manager (systemd, OpenRC, a container runtime, a bare process or
// nothing). Every command goes through an injectable RunFunc; nothing here
// escalates privileges.
package service

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Result is the outcome of a finished command.
type Result struct {
	Stdout []byte
	Stderr []byte
	Exit   int
}

// Output returns stdout and stderr combined.
func (r Result) Output() string {
	return string(bytes.TrimSpace(append(append([]byte{}, r.Stdout...), r.Stderr...)))
}

// RunFunc runs a command to completion. The error is non-nil only when the
// command could not be started (not found, context cancelled before start);
// a non-zero exit is reported in Result.Exit.
type RunFunc func(ctx context.Context, name string, args ...string) (Result, error)

// Exec is the real RunFunc.
func Exec(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.Exit = ee.ExitCode()
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, nil
	}
	return res, err
}
