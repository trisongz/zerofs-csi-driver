# Architecture

## High-level lifecycle

When a workload mounts a PVC provisioned by this driver:

1. **Controller** provisions the PV/PVC using the configured `StorageClass`.
2. **NodePublishVolume** runs on the node where the workload is scheduled.
3. The node plugin creates a per-volume **ZeroFS pod** in the `zerofs` namespace on the same node.
4. The node plugin mounts ZeroFS’ **9P** export to a staging path, creates a sparse `.nbd/disk` file, then unmounts 9P.
5. The node plugin attaches `.nbd/disk` via **NBD** (`nbd-client`) as `/dev/nbdX`.
6. If the device is fresh, it runs `mkfs.<filesystem>` and then mounts the filesystem to the pod’s kubelet mountpoint.

On unpublish/unmount, these steps are reversed: unmount filesystem, detach NBD, and delete the per-volume pod + secret.

## Why 9P + NBD?

- **9P** gives a POSIX-ish interface for creating and manipulating the backing disk file.
- **NBD** turns that backing file into a kernel block device so normal filesystems and workloads can use it.

## Kubernetes objects created

Typical objects involved:
- `Deployment/zerofs-csi-controller` + `DaemonSet/zerofs-csi-node` in `zerofs`
- Per-volume `Pod/zerofs-volume-pvc-...` in `zerofs`
- Per-volume `Secret/zerofs-config-pvc-...` in `zerofs` (generated config)

## Known constraints

- Host nodes must expose `/dev/nbd*` and support `nbd` (the validation harness mounts host `/dev` into k3d nodes).
- Tail latency can be dominated by S3 endpoint behavior; treat durability-sensitive workloads as “benchmark first”.
