//go:build windows

package compat

import (
	"runtime"
	"syscall"
	"unsafe"
)

type windowsVersion struct {
	Size        uint32
	Major       uint32
	Minor       uint32
	Build       uint32
	Platform    uint32
	ServicePack [128]uint16
}

func hostVersion() error {
	if runtime.GOARCH != "amd64" {
		return Fail("E_HOST_UNSUPPORTED", "Windows 11 amd64 is required")
	}
	version := windowsVersion{Size: uint32(unsafe.Sizeof(windowsVersion{}))}
	proc := syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion")
	status, _, _ := proc.Call(uintptr(unsafe.Pointer(&version)))
	if status != 0 {
		return Fail("E_HOST_UNSUPPORTED", "Windows version query failed with status 0x%x", status)
	}
	if version.Major < 10 || version.Build < 22000 {
		return Fail("E_HOST_UNSUPPORTED", "Windows 11 build 22000 or later is required")
	}
	return nil
}
