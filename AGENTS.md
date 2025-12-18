# Repository Guidelines

## Project Structure & Module Organization

- `cmd/driver/`: CSI driver entrypoint (`main.go`) and flag wiring.
- `pkg/csi/`: core CSI services and ZeroFS lifecycle logic (`controller.go`, `node.go`, `zerofs.go`).
- `deploy/`: primary Kubernetes manifests for installing the driver (see `deploy/zerofs.yaml`).
- `validation/`: end-to-end validation manifests (k3d + in-cluster MinIO + benchmarks), plus optional patches and external-S3 configs.
- `out/`: build output directory for platform binaries (generated; do not commit).

## Build, Test, and Development Commands

- `go build ./cmd/driver`: quick local compile check.
- `mkdir -p out && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o out/zerofs-csi-driver-linux-amd64 ./cmd/driver`: build the linux binary expected by `Dockerfile`.
- `docker build -t halceon/zerofs-csi-driver:local .`: build an image locally.
- `make setup-infra`: creates `k3d` cluster `zerofs-test` and deploys in-cluster MinIO (mounts host `/dev` into nodes for `/dev/nbd*`).
- `make build`: builds and pushes `$(IMAGE_NAME)` (default `halceon/zerofs-csi-driver:latest`) for `linux/$(ARCH)`.
- `make deploy`: imports the image into k3d and deploys the driver + config from `validation/`.
- `make test`: runs the basic fio pod from `validation/fio.yaml`.
- `make bench`: runs the mirrored `ZeroFS/bench` suite (image is built locally and imported into k3d).
- `make logs`: collects controller/node/bench logs into `validation-logs.txt`.
- `make teardown`: deletes the k3d cluster.

## Coding Style & Naming Conventions

- Go: run `gofmt` on all `.go` files; keep exported identifiers and comments idiomatic.
- Errors/logging: prefer actionable messages (PVC/volume IDs, node, namespace) and avoid leaking secrets.
- Kubernetes YAML: use stable resource names; keep the `zerofs` namespace consistent with existing manifests; reference `Secret`s by name (do not inline credentials).

## Testing Guidelines

- There are currently no unit tests; use `go test ./...` as a compile sanity check and `go vet ./...` for basic static analysis.
- E2E validation is manifest-driven via `validation/`. For external MinIO/S3-like endpoints, use `validation/zerofs-external-config.yaml` and create the referenced secrets in-cluster (e.g. `halceon-s3-credentials`, `zerofs-encryption-password`).

## Commit & Pull Request Guidelines

- Commits in this repo use short, imperative summaries (e.g., “Fix volume size reporting”). Add a body when behavior or manifests change.
- PRs should include: what changed, how you tested (commands + environment), and any manifest impacts (`deploy/` / `validation/`).
- Avoid committing credentials; reference Kubernetes `Secret`s by name (see `validation/zerofs-config.yaml` and `validation/zerofs-external-config.yaml`).
