# Validation & Benchmarks

## Mirrored `ZeroFS/bench` suite

The mirrored suite matches the one used on zerofs.net. It is useful for *relative* comparisons, but it is not durability-sensitive (it does not fsync per operation).

Run:

```bash
make bench
```

## Durability-sensitive fio benchmarks

For an external MinIO/S3 endpoint, create a `StorageClass` that references secrets by name (see `validation/zerofs-external-config.yaml`), then run:

```bash
make test-external        # btrfs (zerofs-external)
make test-external-ext4   # ext4  (zerofs-external-ext4)
```

These fio jobs include a prefill step so later random reads don’t hit unwritten extents.

## Reference results (external MinIO)

These numbers are **not a guarantee**. They are provided to set expectations and to help detect regressions. Your results will vary with S3 latency/throttling, node CPU/memory, and filesystem choice.

Environment (example run on 2025-12-18):
- k3d cluster, `https://s3.halceon.io`, bucket `s3://zerofs-validation`
- ZeroFS `ghcr.io/barre/zerofs:0.22.4`

**Durability-sensitive fio (4k + fsync)** (higher is better, lower latency is better):

| StorageClass | Job | Read IOPS | Write IOPS | p99 clat |
|---|---|---:|---:|---:|
| `zerofs-external` (btrfs) | `randread_4k_iodepth32` | ~335 | 0 | ~379ms |
| `zerofs-external` (btrfs) | `randwrite_4k_fsync1` | 0 | ~24.8 | ~5.6ms |
| `zerofs-external-ext4` (ext4) | `randread_4k_iodepth32` | ~1.5 | 0 | ~14.3s |
| `zerofs-external-ext4` (ext4) | `randwrite_4k_fsync1` | 0 | ~0.18 | ~2.4s |

**Soak (15 minutes)**: `randrw` 4k, `--fsync=16`, with a 2-minute injected S3 outage mid-run:
- ~2.8 read IOPS / ~1.2 write IOPS, p99 clat ~3.7s

Interpretation:
- Filesystem choice matters. In this environment, `btrfs` was dramatically better than `ext4` for fsync-heavy IO.
- Tail latency can spike into seconds during endpoint brownouts/outages (expected for durability-sensitive workloads on S3).

## Soak testing

Use the long-running Job to catch tail-latency and stability issues:

```bash
make soak
```

The soak manifest is `validation/fio-soak.yaml` (defaults to `zerofs-test` and runs for 1 hour).

## NBD leak regression (attach/detach)

To prevent `/dev/nbd*` leaks on repeated kubelet retries and attach/detach cycles, the repo includes a regression harness that:
- repeatedly mounts/unmounts a PVC on a single node
- asserts the number of *connected* NBD devices returns to the baseline each iteration

Run:

```bash
ITERATIONS=50 make nbd-regression
```

Failure injection mode (deletes the per-volume ZeroFS pod mid-attach):

```bash
ITERATIONS=50 make nbd-regression-chaos
```

The node plugin also runs a best-effort **startup NBD reconciler**: it detaches devices that are *connected* but not *mounted*. This helps recover from abrupt restarts, but it will not touch mounted volumes.

## Collect logs

```bash
make logs
```

This writes `validation-logs.txt` with controller/node logs and benchmark outputs.
