# TODO (Backlog & Work Tracker)

This file tracks follow-up work items and their status. Keep entries short and actionable; link to code paths and validation steps.

Legend: `[ ]` todo · `[~]` in progress · `[x]` done · `[!]` blocked

## P0 — Correctness (must-fix)

- [x] Fix Secret update with `ResourceVersion` (current update likely fails): `pkg/csi/zerofs.go` (`CreatePod`, Secret update path).
  - Expected: update succeeds on retries; no “metadata.resourceVersion” errors.
  - Validate: mount same PVC twice; watch node logs for Secret updates.
- [x] Fix per-volume pod OwnerReference `APIVersion` for DaemonSet: `pkg/csi/zerofs.go` (`OwnerReferences` should be `apps/v1`).
  - Validate: delete DS and confirm GC behavior for per-volume pods (or at least no warning events).
- [x] Ensure `CreatePod` waits for readiness even when pod already exists on the correct node: `pkg/csi/zerofs.go` (pod exists fast-path).
  - Validate: repeated mount/unmount and `make nbd-regression-chaos`.

## P1 — Stability & Operability

- [x] Investigate “long terminating per-volume pods” and implement bounded cleanup.
  - Targets: improve `RemovePod` behavior and/or add force-delete fallback after timeout.
  - Validate: `ITERATIONS=50 make nbd-regression-chaos` + `make soak`.
- [x] Add startup reconciliation for orphaned NBD devices (after driver/node restart).
  - Idea: scan `/sys/block/nbd*/pid` + mounts; detach orphaned devices; clear stale `/run/zerofs-csi` reservations.
  - Validate: kill node plugin container mid-attach and ensure connected NBD count returns to baseline.
- [x] Improve observability around attach/mount lifecycle.
  - Metrics: mount latency, nbd connect time, mkfs time, failures by stage.
  - Output: Prometheus metrics (or structured logs) with `volumeID`, node, and error stage.

## P2 — Production Features

- [x] Optional data cleanup on PVC deletion (`DeleteVolume`).
  - Guardrails: opt-in parameter (e.g. `deleteDataOnPVCDelete=true`), bucket prefix safety checks.
  - Validate: create PVC, write data, delete PVC, confirm objects removed.
- [x] HA MinIO / fencing errors: document known-good config and add mitigation knobs.
  - Potential knobs: retry/backoff, envoy sidecar profile, ZeroFS config tuning.
  - Notes: documented mitigation knobs and added configurable `rustLog`.
- [ ] (Follow-up) Reproduce against HA MinIO and record restart/fencing patterns.
  - Validate: run against HA MinIO and capture logs/metrics during `make soak`.

## P3 — Packaging / UX / CI

- [x] Helm chart (or Kustomize overlays) for `deploy/` and `validation/`.
  - Goal: easy installation with values for endpoint/credentials/secrets.
- [x] Optional CI for NBD regression on self-hosted runner.
  - Current state: manual/opt-in workflows exist; expand to a hardened job that runs `nbd-regression-chaos` + uploads logs.

## Notes

- Known regression harnesses:
  - `make nbd-regression` / `make nbd-regression-chaos`
  - `make soak`
  - `make logs` → `validation-logs.txt`
