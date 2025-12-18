# CI & Releases

## Docker image build workflow

`.github/workflows/build.yml` builds/pushes Docker images to Docker Hub:

- `halceon/zerofs-csi-driver:latest` on `main` pushes
- `halceon/zerofs-csi-driver:vX.Y.Z` (plus `:vX.Y` and `:vX`) on tag pushes

The workflow expects Docker Hub credentials via repository secrets:
- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

## Weekly upstream ZeroFS checks

`.github/workflows/zerofs-upstream.yaml` runs weekly (and on demand):

1. Reads the latest `Barre/ZeroFS` GitHub release.
2. Compares it to the version currently configured in `validation/zerofs-config.yaml`.
3. If newer, builds/pushes `halceon/zerofs-csi-driver:vX.Y.Z` and `:latest`, then validates with k3d + in-cluster MinIO.

Notes:
- Validation uses `make setup-infra`, `make deploy IMAGE_NAME=…`, and `make test`.
- The workflow patches the in-cluster `ConfigMap` to set `zerofsImage=ghcr.io/barre/zerofs:X.Y.Z` for the run.
