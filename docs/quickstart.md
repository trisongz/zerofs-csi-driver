# Quickstart (0 → 1 Test Run)

This guide gets you from a clean machine to a full end-to-end validation using **k3d + in-cluster MinIO**.

## 1) Prereqs

- Docker
- `kubectl`
- [`k3d`](https://k3d.io/)
- Linux host with NBD support:

```bash
sudo modprobe nbd max_part=100
ls -l /dev/nbd0
```

## 2) Clone and build

```bash
git clone https://github.com/trisongz/zerofs-csi-driver.git
cd zerofs-csi-driver
```

## 3) Run the full local validation

This sequence:
- creates a k3d cluster named `zerofs-test`
- deploys MinIO + a test bucket
- builds & pushes the driver image (defaults to `halceon/zerofs-csi-driver:latest`)
- imports the image into k3d and deploys the driver
- runs fio and prints output
- gathers logs into `validation-logs.txt`

```bash
make setup-infra
make build IMAGE_NAME=halceon/zerofs-csi-driver:latest
make deploy IMAGE_NAME=halceon/zerofs-csi-driver:latest
make test
make logs
```

## 4) Sanity checks

```bash
kubectl get nodes -o wide
kubectl -n zerofs get pods -o wide
kubectl get sc
```

Troubleshooting:

```bash
kubectl -n zerofs logs -l app=zerofs-csi-node --all-containers --tail=200
kubectl -n zerofs logs -l app=zerofs-csi-controller --all-containers --tail=200
```

## 5) Clean up

```bash
make teardown
```

## Optional: benchmarks

- Mirrored `ZeroFS/bench` suite: `make bench`
- Long-running soak test: `make soak`
