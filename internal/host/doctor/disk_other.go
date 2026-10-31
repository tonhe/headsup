//go:build !(linux || darwin)

package doctor

func diskFree(string) (uint64, uint64, error) { return 0, 0, ErrUnsupported }
