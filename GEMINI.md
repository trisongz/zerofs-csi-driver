# ZeroFS CSI Driver

A Kubernetes Container Storage Interface (CSI) driver for [ZeroFS](https://www.zerofs.net/), enabling S3-backed block storage for Kubernetes workloads.

## Project Overview

This driver translates Kubernetes storage operations into ZeroFS commands. It does not perform I/O itself but manages the lifecycle of ZeroFS pods, which bridge S3 storage to local block devices via NBD (Network Block Device).

**Key Components:**
*   **Controller:** Manages `PersistentVolume` lifecycles (Provisioning/Deleting).
*   **Node:** Handles attaching/mounting volumes on worker nodes using `nbd-client`.
*   **ZeroFS Pods:** Helper pods spawned in the `zerofs` namespace that perform the actual S3 I/O and expose NBD sockets.

**Architecture:**
*   **Language:** Go
*   **Runtime:** Kubernetes (Deployments, DaemonSets)
*   **Core Libraries:** `k8s.io/client-go`, `sigs.k8s.io/controller-runtime`, `github.com/container-storage-interface/spec`

## Building and Running

### Prerequisites
*   Go 1.25+
*   Docker (for building the image)
*   Kubernetes cluster (for deployment)

### Build Instructions

The project uses a two-step build process: compiling the Go binary and then packaging it into a Docker image.

1.  **Build Go Binary:**
    ```bash
    mkdir -p out
    # For AMD64
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -v -o out/zerofs-csi-driver-linux-amd64 ./cmd/driver
    # For ARM64
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -v -o out/zerofs-csi-driver-linux-arm64 ./cmd/driver
    ```

2.  **Build Docker Image:**
    ```bash
    docker build -t ghcr.io/jrcichra/zerofs-csi-driver:latest .
    ```

### Deployment

The primary deployment manifest is located at `deploy/zerofs.yaml`.

1.  **Configure:** Check `deploy/zerofs.yaml` and ensure image tags and RBAC settings match your environment.
2.  **Deploy:**
    ```bash
    kubectl apply -f deploy/zerofs.yaml
    ```
3.  **StorageClass:** You need to create a `ConfigMap` with ZeroFS settings and a `StorageClass` referencing it (see `README.md` for examples).

## Project Structure

*   `cmd/driver/`: Entry point for the CSI driver binary (`main.go`).
*   `pkg/csi/`: Core CSI logic.
    *   `driver.go`: gRPC server setup and registration.
    *   `controller.go`: Implementation of CSI Controller service (Provisioning).
    *   `node.go`: Implementation of CSI Node service (Attaching/Mounting).
    *   `zerofs.go`: Logic for managing ZeroFS pods.
*   `deploy/`: Kubernetes manifests.
*   `Dockerfile`: Image build definition (requires pre-built binaries in `out/`).

## Conventions

*   **Controller Runtime:** The project uses `controller-runtime` to manage the driver manager and client.
*   **Logrus:** Used for structured logging.
*   **Flags:** Configuration is passed via command-line flags (e.g., `--controller`, `--node`, `--namespace`).
*   **NBD:** The driver relies on the `nbd` kernel module being available on nodes (loaded via an init container in the DaemonSet).
