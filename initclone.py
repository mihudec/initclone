#!/usr/bin/env python3
"""Populate mounted volumes from rclone remotes described by a YAML manifest."""

from __future__ import annotations

import grp
import logging
import os
from pathlib import Path, PurePosixPath
import pwd
import shlex
import shutil
import subprocess
import sys
import tempfile
from typing import Any

import yaml


LOG = logging.getLogger("initclone")
REQUIRED_KEYS = {"bucket", "source", "dest", "mode", "owner", "group"}


class ConfigError(ValueError):
    """Raised when files.yaml is invalid."""


class UniqueKeyLoader(yaml.SafeLoader):
    pass


def _construct_mapping(loader: UniqueKeyLoader, node: yaml.MappingNode, deep: bool = False) -> dict:
    mapping: dict[Any, Any] = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in mapping:
            raise ConfigError(f"duplicate YAML key: {key}")
        mapping[key] = loader.construct_object(value_node, deep=deep)
    return mapping


UniqueKeyLoader.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, _construct_mapping
)


def parse_mode(value: Any) -> int:
    if isinstance(value, bool):
        raise ConfigError("mode must be an octal value such as 0644 or '0644'")
    if isinstance(value, int):
        mode = value
    elif isinstance(value, str):
        try:
            mode = int(value, 8)
        except ValueError as exc:
            raise ConfigError(f"invalid octal mode {value!r}") from exc
    else:
        raise ConfigError("mode must be an octal integer or string")
    if not 0 <= mode <= 0o7777:
        raise ConfigError(f"mode is outside 0000..7777: {value!r}")
    return mode


def resolve_user(value: Any) -> int:
    if isinstance(value, bool):
        raise ConfigError("owner must be a user name or numeric UID")
    if isinstance(value, int) or (isinstance(value, str) and value.isdecimal()):
        uid = int(value)
        if uid < 0:
            raise ConfigError("owner UID cannot be negative")
        return uid
    if isinstance(value, str) and value:
        try:
            return pwd.getpwnam(value).pw_uid
        except KeyError as exc:
            raise ConfigError(f"unknown owner {value!r}; use a numeric UID for portability") from exc
    raise ConfigError("owner must be a user name or numeric UID")


def resolve_group(value: Any) -> int:
    if isinstance(value, bool):
        raise ConfigError("group must be a group name or numeric GID")
    if isinstance(value, int) or (isinstance(value, str) and value.isdecimal()):
        gid = int(value)
        if gid < 0:
            raise ConfigError("group GID cannot be negative")
        return gid
    if isinstance(value, str) and value:
        try:
            return grp.getgrnam(value).gr_gid
        except KeyError as exc:
            raise ConfigError(f"unknown group {value!r}; use a numeric GID for portability") from exc
    raise ConfigError("group must be a group name or numeric GID")


def resolve_destination(value: Any, root: Path) -> Path:
    if not isinstance(value, str) or not value.strip():
        raise ConfigError("dest must be a non-empty path")
    root = root.resolve()
    raw = PurePosixPath(value)
    if ".." in raw.parts:
        raise ConfigError(f"dest cannot contain '..': {value!r}")
    relative = PurePosixPath(*raw.parts[1:]) if raw.is_absolute() else raw
    destination = root.joinpath(*relative.parts).resolve()
    try:
        destination.relative_to(root)
    except ValueError as exc:
        raise ConfigError(f"dest resolves outside DEST_ROOT: {value!r}") from exc
    if destination == root:
        raise ConfigError("dest cannot be the destination root itself")
    return destination


def load_manifest(path: Path, destination_root: Path) -> list[dict[str, Any]]:
    try:
        raw = yaml.load(path.read_text(encoding="utf-8"), Loader=UniqueKeyLoader)
    except (OSError, yaml.YAMLError) as exc:
        raise ConfigError(f"cannot read {path}: {exc}") from exc
    if not isinstance(raw, list) or not raw:
        raise ConfigError("files.yaml must contain a non-empty top-level list")

    entries: list[dict[str, Any]] = []
    destinations: set[Path] = set()
    for index, item in enumerate(raw, start=1):
        label = f"entry {index}"
        if not isinstance(item, dict):
            raise ConfigError(f"{label} must be a mapping")
        missing = REQUIRED_KEYS - item.keys()
        extra = item.keys() - REQUIRED_KEYS
        if missing:
            raise ConfigError(f"{label} is missing: {', '.join(sorted(missing))}")
        if extra:
            raise ConfigError(f"{label} has unknown keys: {', '.join(sorted(extra))}")

        bucket, source = item["bucket"], item["source"]
        if not isinstance(bucket, str) or not bucket.strip():
            raise ConfigError(f"{label} bucket must be a non-empty rclone remote name")
        if ":" in bucket or any(c.isspace() for c in bucket):
            raise ConfigError(f"{label} bucket must be a valid rclone remote name")
        if not isinstance(source, str) or not source.strip():
            raise ConfigError(f"{label} source must be a non-empty remote path")
        if source.startswith("/") or ".." in PurePosixPath(source).parts:
            raise ConfigError(f"{label} source must be relative and cannot contain '..'")

        destination = resolve_destination(item["dest"], destination_root)
        if destination in destinations:
            raise ConfigError(f"duplicate destination: {item['dest']!r}")
        destinations.add(destination)
        entries.append(
            {
                "remote": f"{bucket}:{source}",
                "destination": destination,
                "mode": parse_mode(item["mode"]),
                "uid": resolve_user(item["owner"]),
                "gid": resolve_group(item["group"]),
            }
        )
    return entries


def apply_metadata(path: Path, mode: int, uid: int, gid: int) -> None:
    if not path.exists():
        raise RuntimeError(f"rclone completed but destination does not exist: {path}")

    def update(item: Path, is_directory: bool) -> None:
        item_mode = mode
        if is_directory:
            # A readable directory must also be searchable.
            item_mode |= ((mode & 0o444) >> 2)
        os.chown(item, uid, gid, follow_symlinks=False)
        if not item.is_symlink():
            os.chmod(item, item_mode)

    if path.is_dir():
        for directory, subdirs, files in os.walk(path):
            directory_path = Path(directory)
            update(directory_path, True)
            for name in subdirs:
                candidate = directory_path / name
                if candidate.is_symlink():
                    update(candidate, True)
            for name in files:
                update(directory_path / name, False)
    else:
        update(path, False)


def main() -> int:
    config_dir = Path(os.getenv("CONFIG_DIR", "/config"))
    manifest_path = Path(os.getenv("FILES_CONFIG", str(config_dir / "files.yaml")))
    rclone_config = Path(os.getenv("RCLONE_CONFIG", str(config_dir / "rclone.conf")))
    destination_root = Path(os.getenv("DEST_ROOT", "/")).resolve()

    try:
        logging.basicConfig(
            level=os.getenv("LOG_LEVEL", "INFO"), format="%(levelname)s %(message)s"
        )
        if not rclone_config.is_file():
            raise ConfigError(f"rclone config does not exist: {rclone_config}")
        entries = load_manifest(manifest_path, destination_root)
        extra_args = shlex.split(os.getenv("RCLONE_ARGS", ""))
        # Projected ConfigMaps are read-only, while OAuth remotes may refresh tokens.
        # Give rclone a private, writable copy for the lifetime of this init run.
        with tempfile.TemporaryDirectory(prefix="initclone-") as temporary:
            working_config = Path(temporary) / "rclone.conf"
            shutil.copyfile(rclone_config, working_config)
            working_config.chmod(0o600)
            for index, entry in enumerate(entries, start=1):
                destination: Path = entry["destination"]
                destination.parent.mkdir(parents=True, exist_ok=True)
                LOG.info("copying %s to %s (%d/%d)", entry["remote"], destination, index, len(entries))
                subprocess.run(
                    [
                        "rclone",
                        "copyto",
                        entry["remote"],
                        str(destination),
                        "--config",
                        str(working_config),
                        *extra_args,
                    ],
                    check=True,
                )
                apply_metadata(destination, entry["mode"], entry["uid"], entry["gid"])
        LOG.info("populated %d destination(s)", len(entries))
        return 0
    except (ValueError, OSError, RuntimeError, subprocess.CalledProcessError) as exc:
        LOG.error("%s", exc)
        return 1


if __name__ == "__main__":
    sys.exit(main())
