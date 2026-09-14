//go:build !linux && !darwin

package integration

import "os"

// This prototype fails closed on platforms without its tested OS-lock adapter.
func lockFile(*os.File) error { return ErrStateInvalid }
