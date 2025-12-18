# Configuration

This driver uses a `ConfigMap` referenced by the `StorageClass` to build the ZeroFS `config.toml`. Secrets are referenced by name (do not inline credentials).

## In-cluster MinIO example

See `validation/zerofs-config.yaml`.

- `Secret/minio-zerofs-credentials` (in `zerofs`):
  - `AWS_ACCESS_KEY_ID`
  - `AWS_SECRET_ACCESS_KEY`
- `Secret/zerofs-encryption-password`:
  - `password`
- `ConfigMap/zerofs-btrfs-config`:
  - `awsEndpoint`: `http://minio.default.svc.cluster.local:9000`
  - `storageURL`: `s3://zerofs`
  - `zerofsImage`: `ghcr.io/barre/zerofs:X.Y.Z`

## External MinIO / S3-compatible endpoint

See `validation/zerofs-external-config.yaml`:

- `awsAllowHTTP: "false"` (TLS)
- `awsEndpoint: https://…`
- `storageURL: s3://<bucket>`
- `awsCredentialsSecretName`: a `Secret` containing `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`

## StorageClass parameters

Example:

```yaml
parameters:
  filesystem: btrfs
  configMapName: zerofs-btrfs-config
  deleteDataOnPVCDelete: "false"
mountOptions:
  - compress-force=zstd
```

Supported filesystems depend on what `mkfs.*` tools are available in the driver container. The `validation/` configs currently exercise `btrfs` and `ext4`.

### `deleteDataOnPVCDelete` (optional)

If set to `"true"`, the controller will attempt to delete the S3 prefix for the volume during `DeleteVolume`.

Notes:
- This is **off by default** in `validation/` (`"false"`).
- The prefix is computed as: `storageURL` + `/<volumeID>` (same layout used by the per-volume ZeroFS config).
- Deletion errors cause `DeleteVolume` to fail (so Kubernetes will retry).

### `rustLog` (optional)

The per-volume ZeroFS pod can be configured with a custom `RUST_LOG` value via the config `ConfigMap`:

```yaml
data:
  rustLog: "zerofs=info,slatedb=info"
```

This can be useful when investigating fencing/restart issues or reducing log volume in production.

### `podReadyTimeoutSeconds` (optional)

The node plugin waits for the per-volume ZeroFS pod to become Ready (9P socket created) before proceeding with 9P/NBD setup. On slower clusters or cold starts, a longer timeout reduces first-attach flakes.

```yaml
data:
  podReadyTimeoutSeconds: "120"
```
