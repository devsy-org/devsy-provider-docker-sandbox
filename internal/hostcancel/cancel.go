package hostcancel

import (
	"context"
	"errors"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func Watch(parent context.Context, path string) (context.Context, func() error, error) {
	if path == "" {
		return parent, func() error { return nil }, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, compat.Fail("E_CANCEL_PROTOCOL", "reserved cancellation marker must have a clean absolute path")
	}
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	stopped := make(chan struct{})
	failure := make(chan error, 1)
	check := func() error {
		parentPath := filepath.Dir(path)
		parentDir, e := os.Lstat(parentPath)
		if e != nil || !parentDir.IsDir() || parentDir.Mode()&os.ModeSymlink != 0 {
			return compat.Fail("E_CANCEL_PROTOCOL", "reserved cancellation directory unavailable or unsafe")
		}
		resolvedParent, e := filepath.EvalSymlinks(parentPath)
		if e != nil {
			return compat.Fail("E_CANCEL_PROTOCOL", "reserved cancellation directory cannot be resolved: %v", e)
		}
		parentDir, e = os.Lstat(resolvedParent)
		if e != nil || !parentDir.IsDir() || parentDir.Mode()&os.ModeSymlink != 0 {
			return compat.Fail("E_CANCEL_PROTOCOL", "resolved cancellation directory unavailable or unsafe")
		}
		if runtime.GOOS != "windows" && parentDir.Mode().Perm()&0077 != 0 {
			return compat.Fail("E_CANCEL_PROTOCOL", "reserved cancellation directory must be private")
		}
		marker, e := os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil || !marker.Mode().IsRegular() {
			return compat.Fail("E_CANCEL_PROTOCOL", "reserved cancellation marker unavailable or unsafe")
		}
		cancel(context.Canceled)
		return nil
	}
	if e := check(); e != nil {
		return nil, nil, e
	}
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := check(); e != nil {
					failure <- e
					cancel(e)
					return
				}
			}
		}
	}()
	return ctx, func() error {
		close(done)
		<-stopped
		select {
		case err := <-failure:
			return err
		default:
			return nil
		}
	}, nil
}
