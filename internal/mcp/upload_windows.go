package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openUploadComponent(parent *os.File, name string, directory bool) (*os.File, error) {
	object, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: object,
		Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ, &attrs, &status, nil, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN, options, 0, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle) //nolint:errcheck // reject and release handle
		return nil, errors.New("upload reparse point or unreadable handle")
	}
	return os.NewFile(uintptr(handle), filepath.Join(parent.Name(), name)), nil
}

func openUploadRoot(path string) (*os.File, error) {
	volume := filepath.VolumeName(path)
	ptr, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	anchor := os.NewFile(uintptr(handle), volume+`\`)
	relative := path[len(volume)+1:]
	if relative == "" {
		return anchor, nil
	}
	defer anchor.Close() //nolint:errcheck // release volume anchor after walking
	return walkUpload(anchor, relative, func(parent *os.File, name string, _ bool) (*os.File, error) {
		return openUploadComponent(parent, name, true)
	})
}

func openLocalUpload(path string) (*os.File, error) { return os.Open(path) }

func rejectUploadHardlink(file *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks > 1 {
		return errors.New("multiply-linked files are not allowed in the upload root")
	}
	return nil
}
