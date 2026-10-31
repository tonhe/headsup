//go:build !unix

package main

import "errors"

func lookPath(string) (string, error) {
	return "", errors.New("sudo is not available on this platform")
}

func execve(string, []string, []string) error {
	return errors.New("sudo is not available on this platform")
}
