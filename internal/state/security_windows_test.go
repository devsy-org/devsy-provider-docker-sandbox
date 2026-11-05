package state

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func windowsStore(t *testing.T) Store {
	t.Helper()
	s := Store{
		Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		Key:  testMachineKey,
	}
	requireNoError(t, s.Ensure())
	return s
}

func TestWindowsPrivateState(t *testing.T) {
	s := windowsStore(t)
	fi, err := os.Lstat(s.Dir())
	requireNoError(t, err)
	requireNoError(t, checkDir(s.Dir(), fi))
	file, err := createStateTemp(s.Dir())
	requireNoError(t, err)
	path := file.Name()
	requireNoError(t, file.Close())
	fi, err = os.Lstat(path)
	requireNoError(t, err)
	requireNoError(t, checkFile(path, fi))
	name, err := windows.UTF16PtrFromString(path)
	requireNoError(t, err)
	sd, err := windows.GetNamedSecurityInfo(
		windows.UTF16PtrToString(name),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	requireNoError(t, err)
	control, _, err := sd.Control()
	requireNoError(t, err)
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("state file DACL inherits permissions")
	}
	lease, err := s.Acquire(context.Background(), "mutation", false)
	requireNoError(t, err)
	requireNoError(t, lease.Close())
}

func TestWindowsRejectOtherUsers(t *testing.T) {
	for _, principal := range []string{"WD", "BU", "AU"} {
		t.Run(principal, func(t *testing.T) {
			for _, directory := range []bool{false, true} {
				s := windowsStore(t)
				path := s.Dir()
				if !directory {
					file, err := createStateTemp(path)
					requireNoError(t, err)
					path = file.Name()
					requireNoError(t, file.Close())
				}
				user, err := windows.GetCurrentProcessToken().GetTokenUser()
				requireNoError(t, err)
				sd, err := windows.SecurityDescriptorFromString(
					"D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;" + principal + ")",
				)
				requireNoError(t, err)
				acl, _, err := sd.DACL()
				requireNoError(t, err)
				requireNoError(
					t,
					windows.SetNamedSecurityInfo(
						path,
						windows.SE_FILE_OBJECT,
						windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
						nil,
						nil,
						acl,
						nil,
					),
				)
				fi, err := os.Lstat(path)
				requireNoError(t, err)
				if directory {
					if err := checkDir(path, fi); err == nil {
						t.Fatal("public directory accepted")
					}
					if err := s.Ensure(); err == nil {
						t.Fatal("public directory repaired instead of rejected")
					}
				} else if err := checkFile(path, fi); err == nil {
					t.Fatal("public file accepted")
				}
			}
		})
	}
}

func TestWindowsRejectStateSymlinks(t *testing.T) {
	for _, directory := range []bool{false, true} {
		s := windowsStore(t)
		target := t.TempDir()
		if !directory {
			target = filepath.Join(target, "target")
			requireNoError(t, os.WriteFile(target, nil, 0o600))
		}
		path := filepath.Join(s.Dir(), "link")
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("runner cannot create symlinks: %v", err)
		}
		if err := checkWindowsPath(path); err == nil {
			t.Fatal("reparse point accepted")
		}
	}
}

func TestWindowsRejectStateJunction(t *testing.T) {
	s := windowsStore(t)
	target := filepath.Join(s.Dir(), "target")
	requireNoError(t, makeStateDir(target))
	file, err := createStateTemp(target)
	requireNoError(t, err)
	name := filepath.Base(file.Name())
	requireNoError(t, file.Close())
	junction := filepath.Join(s.Dir(), "junction")
	output, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target).
		CombinedOutput()
	if err != nil {
		t.Skipf("runner cannot create a junction: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(junction); err != nil {
			t.Error(err)
		}
	})
	if err := checkWindowsPath(junction); err == nil {
		t.Fatal("junction passed state validation")
	}
	if file, err := openStateReadFile(junction); err == nil {
		_ = file.Close()
		t.Fatal("junction opened as a file")
	}
	if _, err := readStateDir(junction); err == nil {
		t.Fatal("junction directory was enumerated")
	}
	if file, err := openStateReadFile(filepath.Join(junction, name)); err == nil {
		_ = file.Close()
		t.Fatal("read traversed a junction ancestor")
	}
	if file, err := openStateFile(
		filepath.Join(junction, "mutation.lock"),
		windows.OPEN_ALWAYS,
	); err == nil {
		_ = file.Close()
		t.Fatal("write traversed a junction ancestor")
	}
}

func TestWindowsReadRejectsSwappedSymlink(t *testing.T) {
	s := windowsStore(t)
	original, err := createStateTemp(s.Dir())
	requireNoError(t, err)
	path := original.Name()
	requireNoError(t, original.Close())
	target, err := createStateTemp(s.Dir())
	requireNoError(t, err)
	requireNoError(t, target.Close())
	info, err := os.Lstat(path)
	requireNoError(t, err)
	requireNoError(t, checkStateFile(path, info))
	requireNoError(t, os.Remove(path))
	if err := os.Symlink(target.Name(), path); err != nil {
		t.Skipf("runner cannot create symlinks: %v", err)
	}
	if file, err := openStateReadFile(path); err == nil {
		_ = file.Close()
		t.Fatal("read followed a symlink after path validation")
	}
}
