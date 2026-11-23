package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func loadIdentities(path string) ([]age.Identity, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("identity_file %s: %w", path, err)
	}
	defer file.Close()
	identities, err := age.ParseIdentities(file)
	if err != nil {
		return nil, fmt.Errorf("identity_file %s: %w", path, err)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("identity_file %s contains no identities", path)
	}
	return identities, nil
}

func decryptFiles(ctx context.Context, entry entry, identities []age.Identity) error {
	// Snapshot the inputs so newly generated files are never decrypted again.
	var paths []string
	err := filepath.WalkDir(entry.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type().IsRegular() && strings.HasSuffix(path, ".age") {
			if filepath.Base(path) == ".age" {
				return fmt.Errorf("cannot decrypt file named .age: %s", path)
			}
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := decryptFile(ctx, path, entry, identities); err != nil {
			return fmt.Errorf("decrypt %s: %w", path, err)
		}
	}
	return nil
}

func decryptFile(ctx context.Context, path string, entry entry, identities []age.Identity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination := strings.TrimSuffix(path, ".age")
	// Never follow output symlinks or replace another encrypted input.
	if strings.HasSuffix(destination, ".age") {
		return fmt.Errorf("plaintext path also has .age suffix: %s", destination)
	}
	if info, err := os.Lstat(destination); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("plaintext destination is not a regular file: %s", destination)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	buffer := bufio.NewReader(input)
	var source io.Reader = buffer
	if prefix, _ := buffer.Peek(len(armor.Header)); string(prefix) == armor.Header {
		source = armor.NewReader(buffer)
	}
	plaintext, err := age.Decrypt(source, identities...)
	if err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".initclone-decrypt-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err := io.Copy(output, &contextReader{ctx, plaintext}); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := applyMetadata(output.Name(), entry.Mode, entry.UID, entry.GID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(output.Name(), destination)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
