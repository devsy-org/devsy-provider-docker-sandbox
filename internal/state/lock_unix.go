//go:build !windows

package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func protectDir(path string) error {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.Chmod(filepath.Base(path), 0o700)
}

func checkDir(_ string, fi os.FileInfo) error {
	if fi.Mode().Perm()&0o077 != 0 {
		return errors.New("provider state directory is accessible to other users")
	}
	return nil
}

func checkFile(_ string, fi os.FileInfo) error {
	if fi.Mode().Perm()&0o077 != 0 {
		return errors.New("state file is accessible to other users")
	}
	return nil
}
func replaceFile(src, dst string) error { return os.Rename(src, dst) }
func syncDir(path string) (err error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	d, err := root.Open(".")
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
	f, e := s.openLockFile(name)
	if e != nil {
		return nil, e
	}
	mode := syscall.LOCK_EX | syscall.LOCK_NB
	if shared {
		mode = syscall.LOCK_SH | syscall.LOCK_NB
	}
	return waitLock(ctx, f, mode)
}

func (s Store) openLockFile(name string) (*os.File, error) {
	path := s.Dir() + string(os.PathSeparator) + name + ".lock"
	fd, e := syscall.Open(
		path,
		syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC,
		0o600,
	)
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
	if e = checkFile(path, fi); e != nil {
		return nil, errors.Join(e, f.Close())
	}
	return f, nil
}

func waitLock(ctx context.Context, f *os.File, mode int) (*Lock, error) {
	for {
		e := syscall.Flock(int(f.Fd()), mode)
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

func makeStateDir(path string) error               { return os.Mkdir(path, 0o700) }
func createStateTemp(dir string) (*os.File, error) { return os.CreateTemp(dir, ".atomic-") }

func checkOpenedStateFile(file *os.File, fi os.FileInfo) error { return checkFile(file.Name(), fi) }
func createStateTempDir(dir string) (string, error)            { return os.MkdirTemp(dir, "call-") }

func openStateReadFile(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.Open(filepath.Base(path))
}

func readStateDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
