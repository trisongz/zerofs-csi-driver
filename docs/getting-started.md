# Getting Started

## Prerequisites

- Docker
- `kubectl`
- [`k3d`](https://k3d.io/)
- Linux host with NBD support (example):

```bash
sudo modprobe nbd max_part=100
ls -l /dev/nbd0
```

## Local end-to-end validation (k3d + MinIO)

From the repo root:

```bash
make setup-infra
make build IMAGE_NAME=halceon/zerofs-csi-driver:latest
make deploy IMAGE_NAME=halceon/zerofs-csi-driver:latest
make test
make bench
make logs
```

Artifacts:
- `validation-logs.txt` (aggregated controller/node/benchmark logs)

## Tear down

```bash
make teardown
```

## Notes

- The validation manifests live in `validation/` (MinIO, driver install, fio, mirrored `ZeroFS/bench`).
- The in-cluster driver deployment uses `imagePullPolicy: Never` and imports images via `k3d image import`.
