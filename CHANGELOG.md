# Changelog

## 0.3.0 — 2026-11-23

- Add optional per-entry `identity_file` for embedded age decryption after copying,
  retaining ciphertext for subsequent rclone runs and atomically writing plaintext.

## 0.2.0 — 2026-11-19

### Breaking changes

- Rename the manifest field `bucket` to `remote`. The value names a configured
  section in `rclone.conf`, not a storage bucket or provider type. Existing
  manifests must rename this key; the old key is no longer accepted.
- Replace the Python entry point with a Go executable. Invoke `initclone` instead
  of `python initclone.py`.

### Added

- Standalone binaries with embedded rclone 1.74.2 and its storage backends; no
  Python runtime or separate rclone installation is required.
- Linux and macOS builds for AMD64 and ARM64, with static Linux binaries.
- Release binary downloads and SHA-256 checksums.
- A Makefile with `build`, `test`, `check`, and `clean` targets, plus configurable
  version, commit, and build-date metadata exposed by `--version`.
- Go tests covering manifest validation, permissions, temporary configuration,
  and real local file and directory transfers.

### Changed

- Build the container with Go and use an Alpine runtime with CA certificates.
- Run Go tests and static analysis in CI.
- Preserve environment-variable configuration, full-manifest validation,
  recursive ownership and permissions, and private writable copies of
  `rclone.conf` for OAuth token refreshes.
- Update documentation and the Kubernetes example for the Go binary and `remote`
  field.
