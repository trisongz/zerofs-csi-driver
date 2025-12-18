# ZeroFS CSI Driver

This project is a Kubernetes CSI driver for [ZeroFS](https://www.zerofs.net/): it presents S3-backed storage as a block device, formats it (e.g. `btrfs`, `ext4`, `xfs`), and mounts it for workloads.

## What this driver does

- Provisions a per-volume ZeroFS pod in the `zerofs` namespace (co-located with the workload node).
- Uses ZeroFS’ 9P + NBD sockets to create a sparse “disk file”, attach it as an NBD device, and mount a filesystem on top.
- Manages lifecycle: mount/unmount, NBD attach/detach, and cleanup of per-volume resources.

## Quick links

- Start here: [Getting Started](getting-started.md)
- Understand the flow: [Architecture](architecture.md)
- Configure S3 + filesystem: [Configuration](configuration.md)
- Run benchmarks/soak tests: [Validation & Benchmarks](benchmarks.md)
- Production guidance: [Operations](operations.md)

## Support & expectations

This repo is actively evolving. Treat this documentation as the “source of truth” for how *this* driver is intended to be built, deployed, and validated.
