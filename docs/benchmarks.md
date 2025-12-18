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

## Collect logs

```bash
make logs
```

This writes `validation-logs.txt` with controller/node logs and benchmark outputs.
