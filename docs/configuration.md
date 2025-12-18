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
mountOptions:
  - compress-force=zstd
```

Supported filesystems depend on what `mkfs.*` tools are available in the driver container. The `validation/` configs currently exercise `btrfs` and `ext4`.
