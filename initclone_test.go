package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const validManifest = `- remote: archive
  source: data/file
  dest: /output/file
  mode: 0644
  owner: 1234
  group: "5678"
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestManifest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "files.yaml")
	writeFile(t, path, validManifest)
	entries, err := loadManifest(path, root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	want := entry{"archive:data/file", "/output/file", filepath.Join(resolved, "output/file"), 0644, 1234, 5678}
	if len(entries) != 1 || entries[0] != want {
		t.Fatalf("got %+v, want %+v", entries, want)
	}
	cases := map[string]string{
		"empty": "[]", "mapping": "remote: archive", "scalar": "- hello",
		"source traversal":   strings.Replace(validManifest, "data/file", "../file", 1),
		"source absolute":    strings.Replace(validManifest, "data/file", "/file", 1),
		"dest traversal":     strings.Replace(validManifest, "/output/file", "output/../file", 1),
		"root":               strings.Replace(validManifest, "/output/file", "/", 1),
		"duplicate key":      strings.Replace(validManifest, "- remote: archive", "- remote: archive\n  remote: other", 1),
		"unknown key":        validManifest + "  extra: true\n",
		"missing":            strings.Replace(validManifest, "  group: \"5678\"\n", "", 1),
		"bad remote":         strings.Replace(validManifest, "archive", "bad:remote", 1),
		"boolean mode":       strings.Replace(validManifest, "0644", "true", 1),
		"negative owner":     strings.Replace(validManifest, "1234", "-1", 1),
		"boolean owner":      strings.Replace(validManifest, "1234", "true", 1),
		"duplicate dest":     validManifest + validManifest,
		"multiple documents": validManifest + "---\n" + validManifest,
		"null source":        strings.Replace(validManifest, "data/file", "null", 1),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			writeFile(t, path, content)
			if _, err := loadManifest(path, root); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestModes(t *testing.T) {
	for _, value := range []string{"0644", "\"0644\"", "420", "0o644"} {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(value), &node); err != nil {
			t.Fatal(err)
		}
		mode, err := parseMode(node.Content[0])
		if err != nil || mode != 0644 {
			t.Fatalf("%s: %o %v", value, mode, err)
		}
	}
	for _, value := range []string{"true", "-1", "8192", "\"0999\"", "[]", "null", "1.5"} {
		var node yaml.Node
		if err := yaml.Unmarshal([]byte(value), &node); err != nil {
			t.Fatal(err)
		}
		if _, err := parseMode(node.Content[0]); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}

func TestDestinationSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(base, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDestination("escape/file", root); err == nil || !strings.Contains(err.Error(), "outside DEST_ROOT") {
		t.Fatalf("%v", err)
	}
	if err := os.Symlink(filepath.Join(base, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDestination("dangling/file", root); err == nil {
		t.Fatal("accepted dangling escape")
	}
	if err := os.Symlink("loop", filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveDestination("loop/file", root); err == nil {
		t.Fatal("accepted symlink loop")
	}
}

func TestMetadata(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	file := filepath.Join(root, "nested/file")
	writeFile(t, file, "contents")
	external := filepath.Join(t.TempDir(), "untouched")
	writeFile(t, external, "outside")
	if err := os.Symlink(external, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := applyMetadata(root, 0640, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{root: 0750, filepath.Dir(file): 0750, file: 0640, external: 0600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode %o, want %o", path, info.Mode().Perm(), want)
		}
	}
	if err := applyMetadata(root, 01755, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(root)
	if info.Mode()&os.ModeSticky == 0 {
		t.Fatal("sticky bit missing")
	}
}

func TestSplitArgs(t *testing.T) {
	got, err := splitArgs(`--include '*.mmdb' --header "X-Test: a b" empty='' a\ b '$HOME' "a\qb"`)
	want := []string{"--include", "*.mmdb", "--header", "X-Test: a b", "empty=", "a b", "$HOME", `a\qb`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%q, %v", got, err)
	}
	for _, value := range []string{"'open", `trailing\`} {
		if _, err := splitArgs(value); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func setupRun(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	writeFile(t, filepath.Join(config, "rclone.conf"), "[archive]\ntype = local\n")
	manifest := strings.ReplaceAll(validManifest, "1234", fmt.Sprint(os.Getuid()))
	manifest = strings.ReplaceAll(manifest, "5678", fmt.Sprint(os.Getgid()))
	writeFile(t, filepath.Join(config, "files.yaml"), manifest)
	return root, map[string]string{"CONFIG_DIR": config, "DEST_ROOT": root, "RCLONE_ARGS": "--retries 1"}
}

func TestRun(t *testing.T) {
	root, env := setupRun(t)
	var temporary string
	calls := 0
	err := run(context.Background(), func(key string) string { return env[key] }, io.Discard, func(ctx context.Context, remote, dest, config string, extra []string) error {
		calls++
		if remote != "archive:data/file" || !reflect.DeepEqual(extra, []string{"--retries", "1"}) {
			t.Fatalf("%s %q", remote, extra)
		}
		if config == filepath.Join(env["CONFIG_DIR"], "rclone.conf") {
			t.Fatal("used original config")
		}
		temporary = filepath.Dir(config)
		info, err := os.Stat(config)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private config: %v", err)
		}
		writeFile(t, config, "refreshed")
		writeFile(t, dest, "data")
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("calls %d: %v", calls, err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatal("temporary config not cleaned")
	}
	original, _ := os.ReadFile(filepath.Join(root, "config/rclone.conf"))
	if string(original) != "[archive]\ntype = local\n" {
		t.Fatal("original config changed")
	}
}

func TestRunFailureAndPrevalidation(t *testing.T) {
	_, env := setupRun(t)
	path := filepath.Join(env["CONFIG_DIR"], "files.yaml")
	original, _ := os.ReadFile(path)
	writeFile(t, path, string(original)+strings.Replace(string(original), "/output/file", "../escape", 1))
	calls := 0
	copy := func(context.Context, string, string, string, []string) error {
		calls++
		return errors.New("transfer failed")
	}
	if err := run(context.Background(), func(k string) string { return env[k] }, io.Discard, copy); err == nil || calls != 0 {
		t.Fatalf("validation: calls %d, %v", calls, err)
	}
	writeFile(t, path, string(original)+strings.Replace(string(original), "/output/file", "/second", 1))
	if err := run(context.Background(), func(k string) string { return env[k] }, io.Discard, copy); err == nil || calls != 1 {
		t.Fatalf("failure: calls %d, %v", calls, err)
	}
}

func TestBinaryIntegration(t *testing.T) {
	binary := os.Getenv("INITCLONE_BINARY")
	if binary == "" {
		t.Skip("set INITCLONE_BINARY to a built initclone executable")
	}
	root, env := setupRun(t)
	writeFile(t, filepath.Join(root, "data/file"), "actual rclone transfer")
	writeFile(t, filepath.Join(root, "data/tree/nested/child"), "directory contents")
	manifestPath := filepath.Join(env["CONFIG_DIR"], "files.yaml")
	manifest, _ := os.ReadFile(manifestPath)
	tree := strings.Replace(string(manifest), "data/file", "data/tree", 1)
	tree = strings.Replace(tree, "/output/file", "/output/tree", 1)
	writeFile(t, manifestPath, string(manifest)+tree)
	writeFile(t, filepath.Join(root, "output/tree/keep"), "keep existing")
	command := exec.Command(binary)
	command.Dir = root
	command.Env = []string{"PATH=/nonexistent", "HOME=" + root, "TMPDIR=" + root}
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	for path, want := range map[string]string{"output/file": "actual rclone transfer", "output/tree/nested/child": "directory contents", "output/tree/keep": "keep existing"} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", path, got, err)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, "initclone-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary config leaked: %v", leftovers)
	}
}
