//go:build !windows

package state

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

func protectDir(path string) error { return os.Chmod(path, 0700) }
func checkDir(fi os.FileInfo) error {
	if fi.Mode().Perm()&0077 != 0 {
		return errors.New("provider state directory is accessible to other users")
	}
	return nil
}
func checkFile(fi os.FileInfo) error {
	if fi.Mode().Perm()&0077 != 0 {
		return errors.New("state file is accessible to other users")
	}
	return nil
}
func replaceFile(src, dst string) error { return os.Rename(src, dst) }
func syncDir(path string) (err error) {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	err = d.Sync()
	if errors.Is(err, os.ErrInvalid) {
		err = nil
	}
	return err
}

type Lock struct{ file *os.File }

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	return errors.Join(unlockErr, closeErr)
}
func (s Store) Acquire(ctx context.Context, name string, shared bool) (*Lock, error) {
	if e := s.Ensure(); e != nil {
		return nil, e
	}
	path := s.Dir() + string(os.PathSeparator) + name + ".lock"
	fd, e := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	fi, e := f.Stat()
	if e != nil {
		return nil, errors.Join(e, f.Close())
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.Join(errors.New("unsafe lock file"), f.Close())
	}
	if e = checkFile(fi); e != nil {
		return nil, errors.Join(e, f.Close())
	}
	mode := syscall.LOCK_EX | syscall.LOCK_NB
	if shared {
		mode = syscall.LOCK_SH | syscall.LOCK_NB
	}
	for {
		e = syscall.Flock(int(f.Fd()), mode)
		if e == nil {
			return &Lock{f}, nil
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			return nil, errors.Join(e, f.Close())
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
