package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func applyMetadata(path string, mode uint32, uid, gid int) error {
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("rclone destination: %w", err)
	}
	return filepath.WalkDir(path, func(item string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := os.Lchown(item, uid, gid); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		bits := mode
		if d.IsDir() {
			bits |= (mode & 0444) >> 2
		}
		permissions := os.FileMode(bits & 0777)
		if bits&04000 != 0 {
			permissions |= os.ModeSetuid
		}
		if bits&02000 != 0 {
			permissions |= os.ModeSetgid
		}
		if bits&01000 != 0 {
			permissions |= os.ModeSticky
		}
		return os.Chmod(item, permissions)
	})
}
