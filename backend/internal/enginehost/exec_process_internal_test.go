package enginehost

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestExecStarterCreatesAndSignalsDedicatedProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "host")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	proc, err := (execStarter{hostBin: script}).Start(41001, dir, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = proc.Kill()
		select {
		case <-proc.Done():
		case <-time.After(time.Second):
		}
	})

	pgid, err := syscall.Getpgid(proc.Pid())
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != proc.GroupID() || pgid != proc.Pid() {
		t.Fatalf("pid/pgid/reported = %d/%d/%d, want one dedicated group", proc.Pid(), pgid, proc.GroupID())
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal group: %v", err)
	}
	select {
	case <-proc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("group TERM did not terminate and reap the JVM")
	}
	deadline := time.Now().Add(time.Second)
	for {
		exists, probeErr := proc.GroupExists()
		if probeErr == nil && !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group still exists/error = %v/%v", exists, probeErr)
		}
		time.Sleep(time.Millisecond)
	}
	if err := proc.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal absent group error = %v, want ESRCH", err)
	}
}
