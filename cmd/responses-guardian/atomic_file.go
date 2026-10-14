package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

var errFileChanged = errors.New("configuration changed during update; retry with the latest file")

func atomicWriteFile(path string, data []byte, permission os.FileMode) error {
	return atomicWriteFileChecked(path, data, permission, nil, false)
}

func atomicWriteFileChecked(path string, data []byte, permission os.FileMode, expected []byte, check bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".guardian-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(permission); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if check {
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, expected) {
			return errFileChanged
		}
	}
	return replaceFile(temp.Name(), path)
}
