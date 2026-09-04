//go:build windows

package cmdsurface

// processAlive is not probed on Windows: the subprocess tests that
// need it depend on a POSIX shell and skip there.
func processAlive(int) bool { return false }
