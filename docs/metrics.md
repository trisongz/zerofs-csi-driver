# Metrics

The node plugin exposes Prometheus metrics on `:8080` (same port as controller-runtime metrics). These are intended for debugging performance and spotting NBD lifecycle regressions.

## Scrape / inspect

```bash
kubectl -n zerofs get pods -l app=zerofs-csi-node -o name
kubectl -n zerofs port-forward pod/<zerofs-csi-node-pod> 8080:8080
curl -s localhost:8080/metrics | rg '^zerofs_csi_'
```

## Exported metrics

### `zerofs_csi_node_operation_seconds`

Histogram. Labels:
- `operation`: `NodePublishVolume` | `NodeUnpublishVolume`
- `stage`: a coarse step name (e.g. `create_pod`, `mount_9p`, `create_disk`, `nbd_connect`, `mkfs`, `mount_fs`, `unmount_fs`, `nbd_disconnect`, `remove_pod`)

Use this to track “where time goes” during attach/detach and to alert on tail latency spikes.

### `zerofs_csi_node_errors_total`

Counter. Labels:
- `operation`: `NodePublishVolume` | `NodeUnpublishVolume`
- `stage`: same as above

Use this to quickly identify which step fails most frequently (auth issues, endpoint timeouts, mkfs failures, etc.).

## Suggested queries (PromQL)

```promql
# p99 publish latency by stage (5m window)
histogram_quantile(0.99, sum by (le, stage) (rate(zerofs_csi_node_operation_seconds_bucket{operation="NodePublishVolume"}[5m])))

# error rate by stage
sum by (operation, stage) (rate(zerofs_csi_node_errors_total[5m]))
```

