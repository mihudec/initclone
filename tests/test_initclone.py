import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import yaml

import initclone


class ManifestTests(unittest.TestCase):
    def write_manifest(self, directory: Path, content: str) -> Path:
        path = directory / "files.yaml"
        path.write_text(content, encoding="utf-8")
        return path

    def test_parses_manifest_and_maps_absolute_dest_below_root(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            manifest = self.write_manifest(
                root,
                """- bucket: foo
  source: databases/GeoLite2.mmdb
  dest: /data/GeoLite2.mmdb
  mode: 0644
  owner: 1234
  group: "5678"
""",
            )
            [entry] = initclone.load_manifest(manifest, root)
            self.assertEqual(entry["remote"], "foo:databases/GeoLite2.mmdb")
            self.assertEqual(entry["destination"], root / "data/GeoLite2.mmdb")
            self.assertEqual(entry["mode"], 0o644)
            self.assertEqual((entry["uid"], entry["gid"]), (1234, 5678))

    def test_rejects_traversal_before_copy(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            manifest = self.write_manifest(
                root,
                """- bucket: foo
  source: ../secret
  dest: /data/file
  mode: '0644'
  owner: 0
  group: 0
""",
            )
            with self.assertRaisesRegex(initclone.ConfigError, "source"):
                initclone.load_manifest(manifest, root)

    def test_rejects_duplicate_yaml_keys(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            manifest = self.write_manifest(
                root,
                """- bucket: foo
  bucket: bar
  source: file
  dest: /data/file
  mode: '0644'
  owner: 0
  group: 0
""",
            )
            with self.assertRaisesRegex(initclone.ConfigError, "duplicate YAML key"):
                initclone.load_manifest(manifest, root)

    def test_rejects_destination_symlink_outside_root(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve() / "root"
            root.mkdir()
            (root / "escape").symlink_to(Path(temporary).parent)
            with self.assertRaisesRegex(initclone.ConfigError, "outside DEST_ROOT"):
                initclone.resolve_destination("escape/file", root)


class MetadataTests(unittest.TestCase):
    def test_applies_file_and_searchable_directory_modes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve() / "tree"
            root.mkdir()
            child = root / "file"
            child.write_text("data", encoding="utf-8")
            initclone.apply_metadata(root, 0o640, os.getuid(), os.getgid())
            self.assertEqual(root.stat().st_mode & 0o777, 0o750)
            self.assertEqual(child.stat().st_mode & 0o777, 0o640)


class MainTests(unittest.TestCase):
    def test_runs_rclone_with_bucket_path_and_writable_config_copy(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            config = root / "config"
            config.mkdir()
            (config / "rclone.cfg").write_text("[archive]\ntype = s3\n", encoding="utf-8")
            (config / "files.yaml").write_text(
                f"""- bucket: archive
  source: geo-data/databases/GeoLite2.mmdb
  dest: /output/GeoLite2.mmdb
  mode: "0644"
  owner: "{os.getuid()}"
  group: "{os.getgid()}"
""",
                encoding="utf-8",
            )
            environment = {
                "CONFIG_DIR": str(config),
                "DEST_ROOT": str(root),
            }
            with (
                mock.patch.dict(os.environ, environment, clear=True),
                mock.patch("initclone.subprocess.run") as run,
                mock.patch("initclone.apply_metadata") as metadata,
            ):
                self.assertEqual(initclone.main(), 0)

            command = run.call_args.args[0]
            self.assertEqual(command[:4], [
                "rclone",
                "copyto",
                "archive:geo-data/databases/GeoLite2.mmdb",
                str(root / "output/GeoLite2.mmdb"),
            ])
            self.assertEqual(command[4], "--config")
            self.assertNotEqual(Path(command[5]), config / "rclone.cfg")
            metadata.assert_called_once_with(
                root / "output/GeoLite2.mmdb", 0o644, os.getuid(), os.getgid()
            )


if __name__ == "__main__":
    unittest.main()
