package state

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

func stateSecurity() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString(
		"O:" + user.User.Sid.String() + "D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)",
	)
}

func makeStateDir(path string) error {
	release, err := holdStateParents(path)
	if err != nil {
		return err
	}
	defer release()
	sd, err := stateSecurity()
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.CreateDirectory(
		name,
		&windows.SecurityAttributes{
			Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
			SecurityDescriptor: sd,
		},
	)
}

func createStateTemp(dir string) (*os.File, error) {
	for range 100 {
		token, err := Token()
		if err != nil {
			return nil, err
		}
		file, err := openStateFile(filepath.Join(dir, ".atomic-"+token), windows.CREATE_NEW)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) ||
			errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		return file, err
	}
	return nil, errors.New("cannot allocate state temporary file")
}

func openStateFile(path string, disposition uint32) (*os.File, error) {
	sd, err := stateSecurity()
	if err != nil {
		return nil, err
	}

	handle, err := openCheckedWindowsPath(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL,
		disposition,
		sd,
	)
	if err != nil {
		return nil, err
	}
	return windowsStateFile(handle, path, false)
}

func checkWindowsPath(path string) error {
	handle, err := openCheckedWindowsPath(path, windows.READ_CONTROL, windows.OPEN_EXISTING, nil)
	if err != nil {
		return err
	}
	return windows.CloseHandle(handle)
}

func openCheckedWindowsPath(
	path string,
	access, disposition uint32,
	sd *windows.SECURITY_DESCRIPTOR,
) (windows.Handle, error) {
	release, err := holdStateParents(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	defer release()
	return openWindowsHandle(path, access, disposition, sd)
}

func openWindowsHandle(
	path string,
	access, disposition uint32,
	sd *windows.SECURITY_DESCRIPTOR,
) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	var attributes *windows.SecurityAttributes
	if sd != nil {
		attributes = &windows.SecurityAttributes{
			Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
			SecurityDescriptor: sd,
		}
	}
	handle, err := windows.CreateFile(
		name,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		attributes,
		disposition,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, err
	}
	if err := checkWindowsHandle(handle); err != nil {
		return windows.InvalidHandle, errors.Join(err, windows.CloseHandle(handle))
	}
	return handle, nil
}

func holdStateParents(path string) (func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var parents []string
	for parent := filepath.Dir(absolute); ownedStatePath(parent); parent = filepath.Dir(parent) {
		parents = append(parents, parent)
	}
	var files []*os.File
	release := func() {
		for _, file := range files {
			_ = file.Close()
		}
	}
	for _, parent := range slices.Backward(parents) {
		handle, err := openWindowsHandle(parent, windows.READ_CONTROL, windows.OPEN_EXISTING, nil)
		if err != nil {
			release()
			return nil, err
		}
		file, err := windowsStateFile(handle, parent, true)
		if err != nil {
			release()
			return nil, err
		}
		files = append(files, file)
	}
	return release, nil
}

func windowsStateFile(handle windows.Handle, path string, directory bool) (*os.File, error) {
	file := os.NewFile(uintptr(handle), path)
	info, err := file.Stat()
	if err == nil && (info.IsDir() != directory || (!directory && !info.Mode().IsRegular())) {
		err = errors.New("unexpected state object type")
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}

func openStateReadFile(path string) (*os.File, error) {
	handle, err := openCheckedWindowsPath(
		path,
		windows.GENERIC_READ|windows.READ_CONTROL,
		windows.OPEN_EXISTING,
		nil,
	)
	if err != nil {
		return nil, err
	}
	return windowsStateFile(handle, path, false)
}

func readStateDir(path string) ([]os.DirEntry, error) {
	handle, err := openCheckedWindowsPath(
		path,
		windows.GENERIC_READ|windows.READ_CONTROL,
		windows.OPEN_EXISTING,
		nil,
	)
	if err != nil {
		return nil, err
	}
	file, err := windowsStateFile(handle, path, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(-1)
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return cmp.Compare(a.Name(), b.Name()) })
	return entries, err
}

func checkWindowsHandle(handle windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("state object is a reparse point")
	}
	sd, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return checkWindowsOwnerAndAccess(sd)
}

func checkWindowsOwnerAndAccess(sd *windows.SECURITY_DESCRIPTOR) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil {
		return errors.New("state object has no owner")
	}
	if !owner.Equals(user.User.Sid) {
		allowed, err := administratorOwner(token, owner)
		if err != nil {
			return err
		}
		if !allowed {
			return errors.New("state object is owned by another user")
		}
	}
	return checkWindowsDACL(sd, user.User.Sid)
}

func administratorOwner(token windows.Token, owner *windows.SID) (bool, error) {
	if !token.IsElevated() || !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return false, nil
	}
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenOwner, nil, 0, &size)
	if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return false, err
	}
	data := make([]byte, size)
	if err := windows.GetTokenInformation(
		token,
		windows.TokenOwner,
		&data[0],
		size,
		&size,
	); err != nil {
		return false, err
	}
	defaultOwner := *(**windows.SID)(unsafe.Pointer(&data[0]))
	return defaultOwner != nil && defaultOwner.Equals(owner), nil
}

func checkWindowsDACL(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) error {
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if acl == nil {
		return errors.New("state object has an unrestricted DACL")
	}
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			return err
		}
		if err := checkWindowsACE(ace, user); err != nil {
			return err
		}
	}
	return nil
}

func checkOpenedStateFile(file *os.File, _ os.FileInfo) error {
	return checkWindowsHandle(windows.Handle(file.Fd()))
}

func createStateTempDir(dir string) (string, error) {
	for range 100 {
		token, err := Token()
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, "call-"+token)
		err = makeStateDir(path)
		if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", errors.New("cannot allocate state temporary directory")
}

func checkWindowsACE(ace *windows.ACCESS_ALLOWED_ACE, user *windows.SID) error {
	switch ace.Header.AceType {
	case windows.ACCESS_DENIED_ACE_TYPE:
		return nil
	case windows.ACCESS_ALLOWED_ACE_TYPE:
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) ||
			sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return nil
		}
		return errors.New("state object is accessible to other users")
	default:
		return fmt.Errorf("unsupported state access entry type %d", ace.Header.AceType)
	}
}
