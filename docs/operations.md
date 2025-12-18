# Operations

## Troubleshooting quick checks

```bash
kubectl -n zerofs get pods -o wide
kubectl -n zerofs logs -l app=zerofs-csi-node --all-containers --tail=200
kubectl -n zerofs logs -l app=zerofs-csi-controller --all-containers --tail=200
kubectl get events --sort-by=.lastTimestamp | tail -n 50
```

Metrics (Prometheus format):

```bash
kubectl -n zerofs get pods -l app=zerofs-csi-node -o name
kubectl -n zerofs port-forward pod/<zerofs-csi-node-pod> 8080:8080
curl -s localhost:8080/metrics | rg '^zerofs_csi_'
```

## Common failure modes

- **No `/dev/nbd*` devices**: ensure the host supports NBD and (for k3d) `/dev` is mounted into nodes.
- **Slow mounts / timeouts**: often tied to S3 endpoint latency or throttling; reproduce with `make soak` and inspect tail latencies.
- **Credentials / bucket issues**: confirm secrets exist in `zerofs` and that the bucket is reachable/created.
- **Fencing / restarts (HA MinIO)**: if the per-volume ZeroFS pod repeatedly restarts with fencing errors, treat it as a multi-component issue (ZeroFS + S3/MinIO + config). Start by lowering log verbosity (`rustLog`) and capturing a soak run against the HA endpoint.
- **NBD leak symptoms** (connected `/dev/nbd*` steadily climbs): reproduce with `make nbd-regression` / `make nbd-regression-chaos`, then inspect node logs for cleanup warnings.

## HA MinIO / fencing mitigation checklist

This driver can’t “fix” fencing behavior by itself, but you can reduce churn and improve diagnosability:

- Set `rustLog` in the config `ConfigMap` (e.g. `zerofs=info,slatedb=info`) and only enable debug logs when needed.
- Prefer stable, low-latency networking to the MinIO endpoint; measure with `make soak`.
- Consider enabling the Envoy sidecar only as an explicit mitigation when your S3 endpoint is flaky (note: retries can affect fencing guarantees).

## Security & configuration tips

- Never commit credentials; reference `Secret` names and create them out-of-band.
- Prefer TLS endpoints (`awsAllowHTTP: "false"`) for external storage.
- Use least-privilege IAM policies: bucket-scoped access and only the required object actions.

## Production readiness checklist

- **Resource requests/limits**: set explicit CPU/memory for controller, node, and per-volume pods.
- **Observability**: scrape `/metrics` and alert on tail latency + error rates (e.g. `zerofs_csi_node_operation_seconds`, `zerofs_csi_node_errors_total`).
- **Chaos/failure testing**: restart nodes, kill ZeroFS pods, simulate endpoint outages.
- **Upgrade strategy**: pin `zerofsImage` versions and roll forward with canaries.
