//go:build windows

package main

import "syscall"

// detachSysProcAttr is a no-op on Windows. It exists so the daemon and its test
// harness build and run on a development host, where `launch` is only used by
// the integration tests.
func detachSysProcAttr() *syscall.SysProcAttr {
	return nil
}
