//go:build windows

package state

import (
	"context"
	"errors"
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"os"
	"os/exec"
	"os/user"
	"syscall"
	"time"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var lockFile = kernel32.NewProc("LockFileEx")
var unlockFile = kernel32.NewProc("UnlockFileEx")
var moveFile = kernel32.NewProc("MoveFileExW")

type overlapped struct {
	Internal     uintptr
	InternalHigh uintptr
	Offset       uint32
	OffsetHigh   uint32
	HEvent       syscall.Handle
}

func protectDir(path string) error {
	current, e := user.Current()
	if e != nil {
		return e
	}
	if current.Uid == "" {
		return fmt.Errorf("current Windows SID unavailable")
	}
	cmd := exec.Command("icacls", path, "/grant:r", "*"+current.Uid+":(OI)(CI)F", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F", "/inheritance:r")
	cmd.Env = compat.HostEnvironment()
	out, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("restrict state directory ACL: %w: %s", e, out)
	}
	return nil
}
func checkDir(fi os.FileInfo) error  { return nil }
func checkFile(fi os.FileInfo) error { return nil }
func replaceFile(src, dst string) error {
	a, e := syscall.UTF16PtrFromString(src)
	if e != nil {
		return e
	}
	b, e := syscall.UTF16PtrFromString(dst)
	if e != nil {
		return e
	}
	ok, _, err := moveFile.Call(uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(b)), 0x1|0x8)
	if ok == 0 {
		return err
	}
	return nil
}
func syncDir(path string) error { return nil }

type Lock struct {
	file    *os.File
	overlap overlapped
}

func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	ok, _, unlockErr := unlockFile.Call(l.file.Fd(), 0, 0xffffffff, 0xffffffff, uintptr(unsafe.Pointer(&l.overlap)))
	closeErr := l.file.Close()
	if ok == 0 {
		return errors.Join(unlockErr, closeErr)
	}
	return closeErr
}
func (s Store) Acquire(ctx context.Context, name string, shared bool) (*Lock, error) {
	if e := s.Ensure(); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(s.Dir()+string(os.PathSeparator)+name+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	l := &Lock{file: f}
	flags := uintptr(0x1)
	if !shared {
		flags |= 0x2
	}
	for {
		ok, _, err := lockFile.Call(f.Fd(), flags, 0, 0xffffffff, 0xffffffff, uintptr(unsafe.Pointer(&l.overlap)))
		if ok != 0 {
			return l, nil
		}
		if err != syscall.Errno(33) {
			return nil, errors.Join(err, f.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), f.Close())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (s Store) TryAcquire(name string, shared bool) (*Lock, error) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return s.Acquire(ctx, name, shared)
}
