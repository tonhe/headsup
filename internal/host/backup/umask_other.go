//go:build !unix

package backup

// privateUmask is a no-op where there is no umask.
func privateUmask() func() { return func() {} }
