package main

import (
	"syscall"
	"unsafe"
)

type memoryStatus struct {
	Length, MemoryLoad                                 uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

func totalMemory() uint64 {
	status := memoryStatus{}
	status.Length = uint32(unsafe.Sizeof(status))
	ok, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&status)))
	if ok == 0 {
		return 0
	}
	return status.TotalPhys
}
