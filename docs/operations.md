# Operations

## Troubleshooting quick checks

```bash
kubectl -n zerofs get pods -o wide
kubectl -n zerofs logs -l app=zerofs-csi-node --all-containers --tail=200
kubectl -n zerofs logs -l app=zerofs-csi-controller --all-containers --tail=200
kubectl get events --sort-by=.lastTimestamp | tail -n 50
```

## Common failure modes

- **No `/dev/nbd*` devices**: ensure the host supports NBD and (for k3d) `/dev` is mounted into nodes.
- **Slow mounts / timeouts**: often tied to S3 endpoint latency or throttling; reproduce with `make soak` and inspect tail latencies.
- **Credentials / bucket issues**: confirm secrets exist in `zerofs` and that the bucket is reachable/created.

## Security & configuration tips

- Never commit credentials; reference `Secret` names and create them out-of-band.
- Prefer TLS endpoints (`awsAllowHTTP: "false"`) for external storage.
- Use least-privilege IAM policies: bucket-scoped access and only the required object actions.

## Production readiness checklist

- **Resource requests/limits**: set explicit CPU/memory for controller, node, and per-volume pods.
- **Observability**: metrics + dashboards + alerts for mount latency and error rates.
- **Chaos/failure testing**: restart nodes, kill ZeroFS pods, simulate endpoint outages.
- **Upgrade strategy**: pin `zerofsImage` versions and roll forward with canaries.
