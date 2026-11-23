package main

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

type entry struct {
	Remote, Destination, Path string
	IdentityFile              string
	Mode                      uint32
	UID, GID                  int
}

func loadManifest(path, root string) ([]entry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("cannot read manifest: %w", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("manifest must contain exactly one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.SequenceNode || len(document.Content[0].Content) == 0 {
		return nil, fmt.Errorf("files.yaml must contain a non-empty top-level list")
	}
	var entries []entry
	destinations := map[string]bool{}
	for index, item := range document.Content[0].Content {
		result, err := parseEntry(item, root)
		if err != nil {
			return nil, fmt.Errorf("entry %d: %w", index+1, err)
		}
		if result.IdentityFile != "" && !filepath.IsAbs(result.IdentityFile) {
			result.IdentityFile = filepath.Join(filepath.Dir(path), result.IdentityFile)
		}
		if destinations[result.Path] {
			return nil, fmt.Errorf("duplicate destination: %s", result.Destination)
		}
		destinations[result.Path] = true
		entries = append(entries, result)
	}
	return entries, nil
}

func parseEntry(item *yaml.Node, root string) (entry, error) {
	var result entry
	if item.Kind != yaml.MappingNode {
		return result, fmt.Errorf("must be a mapping")
	}
	keys := map[string]bool{"remote": true, "source": true, "dest": true, "mode": true, "owner": true, "group": true, "identity_file": true}
	values := map[string]*yaml.Node{}
	for i := 0; i < len(item.Content); i += 2 {
		key := item.Content[i]
		if key.Tag != "!!str" || !keys[key.Value] {
			return result, fmt.Errorf("unknown key: %s", key.Value)
		}
		if values[key.Value] != nil {
			return result, fmt.Errorf("duplicate YAML key: %s", key.Value)
		}
		values[key.Value] = item.Content[i+1]
	}
	for _, key := range []string{"remote", "source", "dest", "mode", "owner", "group"} {
		if values[key] == nil {
			return result, fmt.Errorf("missing: %s", key)
		}
	}
	for _, key := range []string{"remote", "source", "dest"} {
		node := values[key]
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.TrimSpace(node.Value) == "" {
			return result, fmt.Errorf("%s must be a non-empty string", key)
		}
	}
	if node := values["identity_file"]; node != nil {
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.TrimSpace(node.Value) == "" {
			return result, fmt.Errorf("identity_file must be a non-empty string")
		}
		result.IdentityFile = node.Value
	}
	remote, source := values["remote"].Value, values["source"].Value
	if strings.Contains(remote, ":") || strings.ContainsFunc(remote, unicode.IsSpace) || strings.HasPrefix(remote, "-") {
		return result, fmt.Errorf("remote must be a valid rclone remote name")
	}
	if strings.HasPrefix(source, "/") || hasParent(source) {
		return result, fmt.Errorf("source must be relative and cannot contain '..'")
	}
	result.Remote = remote + ":" + source
	result.Destination = values["dest"].Value
	var err error
	if result.Path, err = resolveDestination(result.Destination, root); err != nil {
		return result, err
	}
	if result.Mode, err = parseMode(values["mode"]); err != nil {
		return result, err
	}
	if result.UID, err = resolveID(values["owner"], false); err != nil {
		return result, err
	}
	if result.GID, err = resolveID(values["group"], true); err != nil {
		return result, err
	}
	return result, nil
}

func parseMode(node *yaml.Node) (uint32, error) {
	var value int64
	var err error
	switch node.Tag {
	case "!!str":
		value, err = strconv.ParseInt(node.Value, 8, 32)
	case "!!int":
		err = node.Decode(&value)
	default:
		err = fmt.Errorf("expected an octal integer or string")
	}
	if err != nil || value < 0 || value > 07777 {
		return 0, fmt.Errorf("invalid mode %q; use an octal value in 0000..7777", node.Value)
	}
	return uint32(value), nil
}

func resolveID(node *yaml.Node, group bool) (int, error) {
	label := "owner"
	if group {
		label = "group"
	}
	value := node.Value
	if node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!int") || value == "" {
		return 0, fmt.Errorf("%s must be a name or numeric ID", label)
	}
	if node.Tag == "!!int" {
		var id int64
		if err := node.Decode(&id); err != nil || id < 0 || id > 4294967294 {
			return 0, fmt.Errorf("invalid %s ID %q", label, value)
		}
		return int(id), nil
	}
	if strings.Trim(value, "0123456789") != "" {
		if group {
			found, err := user.LookupGroup(value)
			if err != nil {
				return 0, fmt.Errorf("unknown group %q; use a numeric GID for portability", value)
			}
			value = found.Gid
		} else {
			found, err := user.Lookup(value)
			if err != nil {
				return 0, fmt.Errorf("unknown owner %q; use a numeric UID for portability", value)
			}
			value = found.Uid
		}
	}
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil || id == 4294967295 {
		return 0, fmt.Errorf("invalid %s ID %q", label, value)
	}
	return int(id), nil
}

func hasParent(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// Resolve existing symlinks even when the final file (or its parents) is absent.
func resolvePath(path string, depth int) (string, error) {
	if depth > 255 {
		return "", fmt.Errorf("too many symlinks: %s", path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if path == filepath.Dir(path) {
		return path, nil
	}
	parent, err := resolvePath(filepath.Dir(path), depth+1)
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(path))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return path, nil
	}
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(parent, target)
	}
	return resolvePath(target, depth+1)
}

func resolveDestination(value, root string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("dest must be a non-empty path")
	}
	if hasParent(value) {
		return "", fmt.Errorf("dest cannot contain '..': %q", value)
	}
	root, err := resolvePath(root, 0)
	if err != nil {
		return "", err
	}
	destination, err := resolvePath(filepath.Join(root, strings.TrimLeft(value, "/")), 0)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("dest resolves outside DEST_ROOT: %q", value)
	}
	if relative == "." {
		return "", fmt.Errorf("dest cannot be the destination root itself")
	}
	return destination, nil
}
