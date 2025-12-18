#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=hack/nbd_utils.sh
source "${ROOT_DIR}/hack/nbd_utils.sh"

NAMESPACE="${NAMESPACE:-default}"
PVC_NAME="${PVC_NAME:-nbd-regression-pvc}"
POD_NAME="${POD_NAME:-nbd-regression-pod}"
STORAGE_CLASS="${STORAGE_CLASS:-zerofs-test}"
NODE_NAME="${NODE_NAME:-}"
ITERATIONS="${ITERATIONS:-20}"
CHAOS="${CHAOS:-0}" # 1 = delete per-volume pod mid-attach

assert_safe_context

if [[ -z "${NODE_NAME}" ]]; then
  NODE_NAME="$(pick_test_node)"
fi
if [[ -z "${NODE_NAME}" ]]; then
  echo "ERROR: failed to pick a node for testing" >&2
  exit 1
fi

NODE_POD="$(node_plugin_pod_for_node "${NODE_NAME}")"
if [[ -z "${NODE_POD}" ]]; then
  echo "ERROR: failed to find zerofs-csi-node pod on node ${NODE_NAME}" >&2
  exit 1
fi

echo "Using node: ${NODE_NAME}"
echo "Using node plugin pod: ${NODE_POD}"

baseline="$(count_connected_nbd "${NODE_POD}")"
echo "Baseline connected NBD devices: ${baseline}"

cleanup_iteration() {
  kubectl delete pod "${POD_NAME}" -n "${NAMESPACE}" --ignore-not-found >/dev/null 2>&1 || true
  # Keep the PVC so we reuse the same volume id across iterations (tests idempotency).
}

create_pvc_and_pod() {
  kubectl apply -n "${NAMESPACE}" -f - <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${PVC_NAME}
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 2Gi
  storageClassName: ${STORAGE_CLASS}
---
apiVersion: v1
kind: Pod
metadata:
  name: ${POD_NAME}
spec:
  nodeName: ${NODE_NAME}
  restartPolicy: Never
  containers:
    - name: app
      image: alpine:3.22.2
      command: ["sh", "-lc"]
      args:
        - |
          set -euo pipefail
          echo "mounted ok; sleeping"
          sleep 60
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: ${PVC_NAME}
YAML
}

get_volume_id_for_pvc() {
  # Best effort: PV name is usually pvc-<uid> (and is used as CSI volume id).
  kubectl get pvc "${PVC_NAME}" -n "${NAMESPACE}" -o jsonpath='{.spec.volumeName}' 2>/dev/null || true
}

kill_per_volume_pod_once() {
  local volume_id="$1"
  if [[ -z "${volume_id}" ]]; then
    return 0
  fi

  local pod
  # wait briefly for the per-volume pod to exist, then delete it
  for _ in $(seq 1 40); do
    pod="$(kubectl -n zerofs get pod -l type=volume,volume="${volume_id}" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)"
    if [[ -n "${pod}" ]]; then
      echo "CHAOS: deleting per-volume pod ${pod} for volume ${volume_id}"
      kubectl -n zerofs delete pod "${pod}" --grace-period=0 --force >/dev/null 2>&1 || true
      return 0
    fi
    sleep 0.5
  done
  echo "CHAOS: per-volume pod for ${volume_id} not found (skipping)" >&2
}

wait_for_pod_phase() {
  local want="$1"
  local timeout_secs="${2:-120}"
  local start now phase
  start="$(date +%s)"
  while true; do
    phase="$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [[ "${phase}" == "${want}" ]]; then
      return 0
    fi
    now="$(date +%s)"
    if (( now - start >= timeout_secs )); then
      echo "timeout waiting for pod ${POD_NAME} to reach phase ${want} (current=${phase})" >&2
      return 1
    fi
    sleep 1
  done
}

for i in $(seq 1 "${ITERATIONS}"); do
  echo
  echo "=== iteration ${i}/${ITERATIONS} (chaos=${CHAOS}) ==="

  cleanup_iteration

  create_pvc_and_pod

  kubectl wait -n "${NAMESPACE}" --for=jsonpath='{.status.phase}'=Bound "pvc/${PVC_NAME}" --timeout=300s >/dev/null

  volume_id="$(get_volume_id_for_pvc)"
  echo "volume_id=${volume_id}"

  if [[ "${CHAOS}" == "1" ]]; then
    kill_per_volume_pod_once "${volume_id}"
  fi

  # Wait until the workload pod is running (mount succeeded) or fails (mount failed).
  phase=""
  for _ in $(seq 1 180); do
    phase="$(kubectl get pod "${POD_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    if [[ "${phase}" == "Running" || "${phase}" == "Failed" ]]; then
      break
    fi
    sleep 1
  done
  echo "workload pod phase=${phase}"

  kubectl delete pod "${POD_NAME}" -n "${NAMESPACE}" --wait=true --timeout=180s >/dev/null 2>&1 || true

  # Wait for per-volume pod deletion (if it exists).
  if [[ -n "${volume_id}" ]]; then
    kubectl -n zerofs wait --for=delete pod -l type=volume,volume="${volume_id}" --timeout=180s >/dev/null 2>&1 || true
  fi

  if ! wait_for_nbd_count "${NODE_POD}" "${baseline}" 180; then
    echo "NBD leak suspected after iteration ${i}" >&2
    dump_nbd_state "${NODE_POD}"
    echo "Workload pod describe:" >&2
    kubectl describe pod "${POD_NAME}" -n "${NAMESPACE}" || true
    exit 1
  fi

  echo "OK: connected NBD devices returned to baseline (${baseline})"
done

echo
echo "SUCCESS: no NBD leak detected after ${ITERATIONS} iterations (baseline=${baseline})"

