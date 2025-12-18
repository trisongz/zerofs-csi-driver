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
