//go:build !windows

package state

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLockDescriptorCloseOnExec(t *testing.T) {
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	lock, err := s.Acquire(context.Background(), "mutation", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	flags, _, errno := syscall.Syscall(
		syscall.SYS_FCNTL,
		lock.file.Fd(),
		uintptr(syscall.F_GETFD),
		0,
	)
	if errno != 0 {
		t.Fatal(errno)
	}
	if flags&uintptr(syscall.FD_CLOEXEC) == 0 {
		t.Fatal("lock descriptor is inherited across exec")
	}
}
