# initclone

`initclone` is a small init-container image that copies files or directory contents
from rclone remotes into mounted `emptyDir` or `hostPath` volumes, then applies the
requested ownership and permissions.

## Configuration

Mount a ConfigMap at `/config` containing `rclone.conf` and `files.yaml`:

```yaml
- bucket: foo
  source: path_in_bucket
  dest: /path_in_pod
  mode: "0644"
  owner: "1000"
  group: "1000"
```

`bucket` is the rclone remote section name from `rclone.conf`, so this example copies
`foo:path_in_bucket`. For object-storage remotes, `source` can include the actual
bucket or container name, such as `my-s3:database-assets/GeoLite2.mmdb`. `dest` is
the exact destination path. For a directory, rclone copies its contents into
`dest`; it does not delete files already there.

Use numeric UID/GID values in Kubernetes unless the named user and group exist in
the image. A file mode is applied to every copied file. Directories automatically
gain the corresponding search bits (`0644` becomes `0755` for directories).

The entire manifest is validated before the first copy. A failed copy or metadata
operation exits non-zero and prevents the Pod's application containers from
starting. Partial files are managed by rclone using its normal `.partial` behavior.

See [examples/kubernetes.yaml](examples/kubernetes.yaml) for a complete Pod. Keep
credentials in a Secret or workload identity; `rclone.conf` can refer to environment
credentials as shown in the example.

The image makes a mode-`0600` temporary copy of the read-only `rclone.conf`, allowing
rclone to refresh OAuth tokens during the init run without writing to the ConfigMap.

Optional environment variables:

| Variable | Default | Purpose |
| --- | --- | --- |
| `CONFIG_DIR` | `/config` | Directory containing both configuration files |
| `FILES_CONFIG` | `/config/files.yaml` | Manifest path override |
| `RCLONE_CONFIG` | `/config/rclone.conf` | rclone config path override |
| `DEST_ROOT` | `/` | Root below which all destinations are resolved |
| `RCLONE_ARGS` | empty | Additional rclone arguments, shell-split without evaluation |
| `LOG_LEVEL` | `INFO` | Python logging level |

## Build and publish

```sh
docker build -t initclone:dev .
docker run --rm \
  -v "$PWD/config:/config:ro" \
  -v "$PWD/output:/shared" \
  initclone:dev
```

CI tests every pull request and push to `main`. Push a tag such as `v1.0.0` to
publish multi-architecture `1.0.0`, `1.0`, and `latest` images to this repository's
GitHub Container Registry package. The workflow generates provenance and an SBOM.

## Why Python

The wrapper deliberately uses Python rather than Bash plus `yq`. The manifest is
configuration with security-sensitive paths and ownership, so typed validation,
duplicate-key detection, recursive metadata handling, and unit tests are worth the
small runtime cost. rclone still performs all remote I/O.
