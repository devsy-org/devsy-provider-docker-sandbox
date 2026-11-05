//go:build windows

package state

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func protectDir(_ string) error { return nil }
func checkDir(path string, fi os.FileInfo) error {
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe state directory")
	}
	return checkWindowsPath(path)
}

func checkFile(path string, fi os.FileInfo) error {
	if !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe state file")
	}
	return checkWindowsPath(path)
}

func replaceFile(src, dst string) error {
	a, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		a,
		b,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
func syncDir(_ string) error { return nil }

type Lock struct {
	file    *os.File
	overlap windows.Overlapped
}

func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(
		windows.Handle(l.file.Fd()),
		0,
		0xffffffff,
		0xffffffff,
		&l.overlap,
	)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}

func (s Store) Acquire(ctx context.Context, name string, shared bool) (*Lock, error) {
	if e := s.Ensure(); e != nil {
		return nil, e
	}
	f, e := openStateFile(s.Dir()+string(os.PathSeparator)+name+".lock", windows.OPEN_ALWAYS)
	if e != nil {
		return nil, e
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if !shared {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	return waitWindowsLock(ctx, f, flags)
}

func waitWindowsLock(ctx context.Context, f *os.File, flags uint32) (*Lock, error) {
	l := &Lock{file: f}
	for {
		err := windows.LockFileEx(
			windows.Handle(f.Fd()),
			flags,
			0,
			0xffffffff,
			0xffffffff,
			&l.overlap,
		)
		if err == nil {
			return l, nil
		}
		if err != windows.ERROR_LOCK_VIOLATION {
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
