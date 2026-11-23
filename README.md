# initclone

`initclone` is a standalone Go binary and init-container image that copies files or directory contents
from rclone remotes into mounted `emptyDir` or `hostPath` volumes, then applies the
requested ownership and permissions.

## Configuration

Mount a ConfigMap at `/config` containing `rclone.conf` and `files.yaml`:

```yaml
- remote: foo
  source: path_in_bucket
  dest: /path_in_pod
  mode: "0644"
  owner: "1000"
  group: "1000"
```

`remote` is the configured remote section name from `rclone.conf` (for example,
`foo` for `[foo]`), so this example copies `foo:path_in_bucket`. It is not the
storage bucket name or the provider type (such as S3 or AWS). For object-storage
remotes, `source` includes the actual bucket or container name, such as
`database-assets/GeoLite2.mmdb`. `dest` is
the exact destination path. For a directory, rclone copies its contents into
`dest`; it does not delete files already there.

Add optional `identity_file` to an entry to decrypt every regular `.age` file in
its destination after copying (recursively for directories):

```yaml
- remote: foo
  source: configs/app
  dest: /etc/app
  identity_file: /secrets/age-identity.txt
  mode: "0600"
  owner: "1000"
  group: "1000"
```

Identity paths are local paths; relative paths resolve beside `files.yaml`,
independently of `DEST_ROOT`. Mount identity files from a Secret. Native age
identity files are supported (one key per line, with optional comments); SSH,
plugin, and passphrase-protected identities are not supported. No age executable
is required. Both binary and ASCII-armored ciphertext are accepted.

`config.yaml.age` produces `config.yaml` alongside it. Original `.age` files and
their modification times are retained so rclone can skip unchanged ciphertext
on subsequent runs. Plaintext is regenerated each run and atomically replaces
any existing regular file only after successful decryption. Both files receive
the entry's ownership and permissions. Symlinks are not followed; plaintext
output symlinks and ambiguous names such as `.age` or `file.age.age` are rejected.
Without `identity_file`, files are copied unchanged. Invalid identities fail
before any copy; decryption failures stop the run without publishing partial
plaintext.

Use numeric UID/GID values in Kubernetes unless the named user and group exist in
the image. A file mode is applied to every copied file. Directories automatically
gain the corresponding search bits (`0644` becomes `0755` for directories).

The entire manifest is validated before the first copy. A failed copy, decryption, or metadata
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
| `LOG_LEVEL` | `INFO` | Wrapper logging level: DEBUG, INFO, WARNING, ERROR, CRITICAL or NOTSET |

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

## Portable binary

Go 1.27.1 or newer is required to build. rclone 1.74.2 and its storage backends
are compiled into the executable; Python and a separate rclone installation are
not needed. All backends are included, so the executable is larger than a wrapper.

```sh
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/initclone .
CONFIG_DIR="$PWD/config" DEST_ROOT="$PWD/output" ./bin/initclone
```

Cross-compile by setting `GOOS=linux` or `GOOS=darwin` and `GOARCH=amd64` or
`GOARCH=arm64`. Releases include these four binaries and SHA-256 checksums.
Linux binaries are static and do not require a particular libc. HTTPS remotes
still need the host's CA certificates. Named users and groups use the host's
account database; numeric IDs are recommended. Windows is not supported because
this tool applies Unix ownership and permissions. Changing ownership requires
appropriate privileges.

In existing `files.yaml` manifests, rename `bucket` to `remote`. The old key is
no longer accepted. `rclone.conf` and environment variables are unchanged.
`RCLONE_ARGS` uses shell quoting without executing a shell or expanding variables.
`LOG_LEVEL` controls initclone messages; use `RCLONE_ARGS="--log-level DEBUG"`
for transfer diagnostics. Temporary refreshed OAuth tokens are removed at exit,
as before. Each transfer runs in a child instance of the same executable.

## Development

The Makefile follows the same layout for all four portable builds:

```sh
make build                # dist/{macos,linux}/{amd64,arm64}/initclone
make build VERSION=v1.0.0  # Embed version, Git commit, and UTC build date
make test                 # Unit tests
make check                # go vet and race-enabled tests
make clean                # Remove dist/
```

`COMMIT` and `BUILD_DATE` can also be overridden. Run a built binary with
`--version` to inspect its build metadata.

To run the local transfer integration test:

```sh
CGO_ENABLED=0 go build -o bin/initclone .
INITCLONE_BINARY="$PWD/bin/initclone" go test -run TestBinaryIntegration -v .
```

The integration test uses a local rclone remote and does not need cloud credentials.
