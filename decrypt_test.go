package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/armor"
)

func encrypted(t *testing.T, id *age.X25519Identity, armored bool) string {
	t.Helper()
	var data bytes.Buffer
	var target io.Writer = &data
	var a io.WriteCloser
	if armored {
		a = armor.NewWriter(&data)
		target = a
	}
	w, err := age.Encrypt(target, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(w, "secret contents"); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if a != nil {
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return data.String()
}

func TestRunDecryption(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			root, env := setupRun(t)
			id, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(env["CONFIG_DIR"], "identity"), "# identity\n"+id.String()+"\n")
			manifest := filepath.Join(env["CONFIG_DIR"], "files.yaml")
			original, _ := os.ReadFile(manifest)
			dest := "/output/file.age"
			if directory {
				dest = "/output/tree"
			}
			writeFile(t, manifest, strings.Replace(string(original), "/output/file", dest, 1)+"  identity_file: identity\n")
			cipher := encrypted(t, id, directory)
			input := filepath.Join(root, dest)
			if directory {
				input = filepath.Join(input, "nested/file.age")
			}
			copy := func(_ context.Context, _, destination, _ string, _ []string) error {
				if directory {
					destination = filepath.Join(destination, "nested/file.age")
				}
				if _, err := os.Stat(destination); os.IsNotExist(err) {
					writeFile(t, destination, cipher)
				}
				return nil
			}
			for i := 0; i < 2; i++ {
				if err := run(context.Background(), func(k string) string { return env[k] }, io.Discard, copy); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(strings.TrimSuffix(input, ".age"))
				if err != nil || string(got) != "secret contents" {
					t.Fatalf("plaintext %q: %v", got, err)
				}
				retained, _ := os.ReadFile(input)
				if string(retained) != cipher {
					t.Fatal("ciphertext changed")
				}
				stat, _ := os.Stat(strings.TrimSuffix(input, ".age"))
				if stat.Mode().Perm() != 0644 {
					t.Fatal("incorrect plaintext mode")
				}
			}
		})
	}
}

func TestDecryptionFailurePreservesPlaintext(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	for name, cipher := range map[string]string{"wrong identity": encrypted(t, other, false), "corruption": encrypted(t, id, false)[:100], "payload corruption": encrypted(t, id, false) + "bad"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file.age")
			writeFile(t, path, cipher)
			writeFile(t, filepath.Join(root, "file"), "previous")
			e := entry{Path: path, Mode: 0644, UID: os.Getuid(), GID: os.Getgid()}
			if err := decryptFiles(context.Background(), e, []age.Identity{id}); err == nil {
				t.Fatal("accepted invalid ciphertext")
			}
			got, _ := os.ReadFile(filepath.Join(root, "file"))
			if string(got) != "previous" {
				t.Fatal("overwrote previous plaintext")
			}
			leftovers, _ := filepath.Glob(filepath.Join(root, ".initclone-decrypt-*"))
			if len(leftovers) > 0 {
				t.Fatal("temporary plaintext leaked")
			}
		})
	}
}

func TestIdentityValidationBeforeCopy(t *testing.T) {
	for _, value := range []string{"null", "true", "[]", `""`, `missing`} {
		t.Run(value, func(t *testing.T) {
			_, env := setupRun(t)
			path := filepath.Join(env["CONFIG_DIR"], "files.yaml")
			original, _ := os.ReadFile(path)
			writeFile(t, path, string(original)+"  identity_file: "+value+"\n")
			err := run(context.Background(), func(k string) string { return env[k] }, io.Discard, func(context.Context, string, string, string, []string) error {
				t.Fatal("copy before validation")
				return nil
			})
			if err == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
}

func TestDecryptionSymlinks(t *testing.T) {
	root := t.TempDir()
	id, _ := age.GenerateX25519Identity()
	external := filepath.Join(t.TempDir(), "external")
	writeFile(t, external, "untouched")
	writeFile(t, filepath.Join(root, "file.age"), encrypted(t, id, false))
	if err := os.Symlink(external, filepath.Join(root, "file")); err != nil {
		t.Fatal(err)
	}
	e := entry{Path: root, Mode: 0600, UID: os.Getuid(), GID: os.Getgid()}
	if err := decryptFiles(context.Background(), e, []age.Identity{id}); err == nil {
		t.Fatal("accepted output symlink")
	}
	got, _ := os.ReadFile(external)
	if string(got) != "untouched" {
		t.Fatal("modified external file")
	}
}
