#!/usr/bin/env bash
set -euo pipefail

FORBIDDEN_NODES=(
  "metal-a16c96g-mf795s7-001"
  "metal-a8c64g-mfum690s-001"
  "metal-i14c64g-mfnab9-001"
  "metal-i14c64g-mfnad9-001"
)

assert_safe_context() {
  local current_ctx
  current_ctx="$(kubectl config current-context 2>/dev/null || true)"
  if [[ -z "${current_ctx}" ]]; then
    echo "ERROR: kubectl current-context is empty" >&2
    exit 1
  fi
  if [[ "${current_ctx}" != k3d-* ]]; then
    echo "ERROR: expected a k3d-* kube context, got: ${current_ctx}" >&2
    exit 1
  fi

  local nodes
  nodes="$(kubectl get nodes -o name 2>/dev/null || true)"
  for n in "${FORBIDDEN_NODES[@]}"; do
    if echo "${nodes}" | grep -q "${n}"; then
      echo "ERROR: forbidden node '${n}' detected; refusing to proceed" >&2
      exit 1
    fi
  done
}

pick_test_node() {
  # Prefer an agent node if present; otherwise pick the first Ready node.
  local node
  node="$(kubectl get nodes -o jsonpath='{range .items[?(@.metadata.name matches ".*agent.*")]}{.metadata.name}{"\n"}{end}' | head -n 1 || true)"
  if [[ -z "${node}" ]]; then
    node="$(kubectl get nodes -o jsonpath='{range .items[?(@.status.conditions[?(@.type=="Ready")].status=="True")]}{.metadata.name}{"\n"}{end}' | head -n 1)"
  fi
  echo "${node}"
}

node_plugin_pod_for_node() {
  local node="$1"
  kubectl -n zerofs get pod -l app=zerofs-csi-node -o jsonpath="{range .items[?(@.spec.nodeName==\"${node}\")]}{.metadata.name}{\"\n\"}{end}" | head -n 1
}

count_connected_nbd() {
  local pod="$1"
  # Count devices where /sys/block/nbd*/pid > 0.
  kubectl -n zerofs exec "${pod}" -c zerofs-csi-node -- sh -lc '
    count=0
    for f in /sys/block/nbd*/pid; do
      [ -e "$f" ] || continue
      pid="$(cat "$f" 2>/dev/null || echo 0)"
      case "$pid" in
        "" ) pid=0 ;;
      esac
      if [ "$pid" -gt 0 ] 2>/dev/null; then
        count=$((count+1))
      fi
    done
    echo "$count"
  '
}

dump_nbd_state() {
  local pod="$1"
  echo "---- connected NBD devices (sysfs pid) ----"
  kubectl -n zerofs exec "${pod}" -c zerofs-csi-node -- sh -lc '
    for f in /sys/block/nbd*/pid; do
      [ -e "$f" ] || continue
      dev="$(basename "$(dirname "$f")")"
      pid="$(cat "$f" 2>/dev/null || true)"
      echo "${dev} pid=${pid}"
    done
  ' || true

  echo "---- /run/zerofs-csi state (best-effort) ----"
  kubectl -n zerofs exec "${pod}" -c zerofs-csi-node -- sh -lc '
    if [ -d /run/zerofs-csi ]; then
      find /run/zerofs-csi -maxdepth 2 -type f -print -exec sh -lc "echo -n \"  \"; cat \"$1\" 2>/dev/null || true" _ {} \;
    else
      echo "missing /run/zerofs-csi"
    fi
  ' || true
}

wait_for_nbd_count() {
  local pod="$1"
  local expected="$2"
  local timeout_secs="${3:-120}"

  local start now cur
  start="$(date +%s)"
  while true; do
    cur="$(count_connected_nbd "${pod}")"
    if [[ "${cur}" == "${expected}" ]]; then
      return 0
    fi
    now="$(date +%s)"
    if (( now - start >= timeout_secs )); then
      echo "ERROR: timed out waiting for connected NBD count=${expected} (current=${cur})" >&2
      return 1
    fi
    sleep 2
  done
}

