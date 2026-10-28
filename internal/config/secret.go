package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/tonhe/fathom/internal/api"
	"github.com/tonhe/fathom/internal/brand"
)

// KeyCmdTimeout bounds api_key_cmd. A variable so tests can shorten it.
var KeyCmdTimeout = 10 * time.Second

// ErrNoKey means the profile has no key source configured.
var ErrNoKey = errors.New("no API key configured")

// ResolveKey fetches the API key from p.Key. The raw value is never placed
// in an error or log line.
func ResolveKey(p ResolvedProfile, env func(string) string) (api.Secret, error) {
	if env == nil {
		env = getenv
	}
	var raw string
	switch p.Key.Kind {
	case KeyEnv:
		raw = strings.TrimSpace(env(p.Key.Env))
		if raw == "" {
			return api.Secret{}, fmt.Errorf("API key variable %s is empty or unset", p.Key.Env)
		}
	case KeyFile:
		v, err := readKeyFile(p.Key.File)
		if err != nil {
			return api.Secret{}, err
		}
		raw = v
	case KeyCmd:
		v, err := runKeyCmd(p.Key.Cmd)
		if err != nil {
			return api.Secret{}, err
		}
		raw = v
	case KeyKeyring:
		if p.Name == "" {
			return api.Secret{}, errors.New("keyring lookup needs a profile name")
		}
		v, err := keyring.Get(brand.Name, p.Name)
		if errors.Is(err, keyring.ErrNotFound) {
			return api.Secret{}, fmt.Errorf("no API key in the OS keyring for %s/%s", brand.Name, p.Name)
		}
		if err != nil {
			return api.Secret{}, fmt.Errorf("keyring %s/%s: %w", brand.Name, p.Name, err)
		}
		raw = strings.TrimSpace(v)
	default:
		return api.Secret{}, ErrNoKey
	}
	if raw == "" {
		return api.Secret{}, fmt.Errorf("API key from %s is empty", p.Key.Kind)
	}
	return api.NewSecret(raw), nil
}

// StoreKeyring saves s in the OS keyring under service brand.Name, user profile.
func StoreKeyring(profile string, s api.Secret) error {
	if profile == "" {
		return errors.New("keyring store needs a profile name")
	}
	if s.IsZero() {
		return errors.New("refusing to store an empty API key")
	}
	if err := keyring.Set(brand.Name, profile, s.Reveal()); err != nil {
		return fmt.Errorf("keyring %s/%s: %w", brand.Name, profile, err)
	}
	return nil
}

func readKeyFile(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("api_key_file: %w", err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("api_key_file %s is a directory", path)
	}
	if goos != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("api_key_file %s is accessible by group or others (mode %04o); fix with: chmod 600 %s",
			path, fi.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("api_key_file: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func runKeyCmd(cmdline string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), KeyCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdline)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("api_key_cmd timed out after %s", KeyCmdTimeout)
	}
	if err != nil {
		// stderr is shown (trimmed) to aid debugging; stdout never is.
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		if msg != "" {
			return "", fmt.Errorf("api_key_cmd failed: %v: %s", err, msg)
		}
		return "", fmt.Errorf("api_key_cmd failed: %v", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}
