//go:build unix

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// probeSignal logs every process-group signal the harness sends (KU 807 probe).
func probeSignal(pid int, signal syscall.Signal) {
	_, file, line, _ := runtime.Caller(2)
	fmt.Fprintf(os.Stderr, "ku807-probe %s harness signals group %d with %s from %s:%d\n", time.Now().UTC().Format(time.RFC3339Nano), pid, signal, file, line)
}

// ConfigureProcessGroup prepares command for process-group signaling.
func ConfigureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// RunAs makes the command run as uid:gid with no supplementary groups.
func RunAs(command *exec.Cmd, uid, gid uint32) {
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.Credential = &syscall.Credential{Uid: uid, Gid: gid}
}

// InterruptProcessGroup interrupts the process group.
func InterruptProcessGroup(process *os.Process) error {
	probeSignal(process.Pid, syscall.SIGINT)
	return syscall.Kill(-process.Pid, syscall.SIGINT)
}

// TerminateProcessGroup terminates the process group.
func TerminateProcessGroup(process *os.Process) error {
	probeSignal(process.Pid, syscall.SIGTERM)
	return syscall.Kill(-process.Pid, syscall.SIGTERM)
}

// KillProcessGroup kills the process group.
func KillProcessGroup(process *os.Process) error {
	probeSignal(process.Pid, syscall.SIGKILL)
	return syscall.Kill(-process.Pid, syscall.SIGKILL)
}
