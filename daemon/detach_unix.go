//go:build !windows

package main

import "syscall"

// detachSysProcAttr puts the launched daemon in its own session so it survives
// the root shell that started it (libsu's shell exits as soon as the command
// returns, and a SIGHUP to the process group would otherwise kill the daemon).
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
