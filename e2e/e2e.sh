#!/usr/bin/env bash
set -euo pipefail

namespace="${SUNDAY_NAMESPACE:-sunday-system}"
app_name="${SUNDAY_APP_NAME:-sunday-app}"
crash_name="sunday-e2e-crash"
client_label="sunday-e2e-client"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "required command is missing: kubectl" >&2
  exit 1
fi

cleanup() {
  kubectl delete etherealpod "$crash_name" -n "$namespace" \
    --ignore-not-found --wait=false >/dev/null 2>&1 || true
  kubectl delete pod -n "$namespace" \
    -l "app.kubernetes.io/name=$client_label" \
    --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

api_request() {
  local allow_not_found=false
  if [[ "${1:-}" == "--allow-not-found" ]]; then
    allow_not_found=true
    shift
  fi
  local client_name="sunday-e2e-client-$$-$RANDOM"
  local response="" phase="" status=""
  local deadline=$((SECONDS + 90))

  # Retain logs and observe both terminal phases: failed clients must fail fast.
  kubectl run "$client_name" -n "$namespace" \
    --restart=Never \
    --labels="app.kubernetes.io/name=$client_label" \
    --image=curlimages/curl:8.12.1 \
    --command -- curl --silent --show-error --connect-timeout 5 --max-time 20 \
    --write-out '\n%{http_code}' "$@" >/dev/null

  while (( SECONDS < deadline )); do
    phase="$(kubectl get pod "$client_name" -n "$namespace" \
      -o jsonpath='{.status.phase}')" || return 1
    if [[ "$phase" == "Succeeded" || "$phase" == "Failed" ]]; then
      break
    fi
    sleep 1
  done
  if [[ "$phase" != "Succeeded" ]]; then
    echo "API client finished in phase $phase (or timed out)" >&2
    kubectl logs "$client_name" -n "$namespace" >&2 || true
    kubectl describe pod "$client_name" -n "$namespace" >&2 || true
    return 1
  fi

  response="$(kubectl logs "$client_name" -n "$namespace")" || return 1
  kubectl delete pod "$client_name" -n "$namespace" \
    --ignore-not-found --wait=false >/dev/null
  status="${response##*$'\n'}"
  response="${response%$'\n'*}"
  if [[ ! "$status" =~ ^2[0-9][0-9]$ ]] && \
    ! { [[ "$allow_not_found" == true && "$status" == 404 ]]; }; then
    echo "API returned HTTP $status: $response" >&2
    return 1
  fi
  printf '%s' "$response"
}

wait_for_service() {
  # Pod readiness can precede Service routing updates. Retry only this safe GET;
  # mutation requests must never be retried after an uncertain response.
  api_request --retry 15 --retry-delay 1 --retry-connrefused --retry-max-time 30 \
    "http://$app_name/healthz" >/dev/null
}

wait_for_replacement() {
  local old_pod="$1"
  local new_pod=""

  for _ in {1..60}; do
    new_pod="$(kubectl get etherealpod "$app_name" -n "$namespace" \
      -o jsonpath='{.status.podName}' 2>/dev/null || true)"
    if [[ -n "$new_pod" && "$new_pod" != "$old_pod" ]] && \
      kubectl wait pod "$new_pod" -n "$namespace" \
        --for=condition=Ready --timeout=2s >/dev/null 2>&1; then
      wait_for_service || return 1
      printf '%s' "$new_pod"
      return 0
    fi
    sleep 2
  done

  echo "replacement Pod did not become Ready" >&2
  return 1
}

wait_for_restart_count() {
  local restarts=""

  for _ in {1..60}; do
    restarts="$(kubectl get etherealpod "$crash_name" -n "$namespace" \
      -o jsonpath='{.status.restarts}' 2>/dev/null || true)"
    if [[ "$restarts" =~ ^[0-9]+$ ]] && (( restarts > 0 )); then
      printf '%s' "$restarts"
      return 0
    fi
    sleep 2
  done

  echo "restart count did not increase" >&2
  return 1
}

echo "Checking the deployed EtherealPod and printer columns..."
kubectl wait etherealpod "$app_name" -n "$namespace" \
  --for=condition=Ready --timeout=120s >/dev/null
kubectl get eps -n "$namespace" | \
  grep -Eq '^NAME[[:space:]]+AGE[[:space:]]+RESTARTS'

wait_for_service

# Make the persistence assertion repeatable when the script is run more than once.
api_request --allow-not-found -X DELETE \
  "http://$app_name/delete_product?product_name=testproduct" >/dev/null

echo "Writing a grocery item..."
write_response="$(api_request -X POST \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"testuser","product_name":"testproduct","amount":7}' \
  "http://$app_name/write")"
if [[ "$write_response" != *'"total":7'* ]]; then
  echo "unexpected write response: $write_response" >&2
  exit 1
fi

old_pod="$(kubectl get etherealpod "$app_name" -n "$namespace" \
  -o jsonpath='{.status.podName}')"
if [[ -z "$old_pod" ]]; then
  echo "EtherealPod status does not contain podName" >&2
  exit 1
fi

echo "Deleting $old_pod and waiting for self-healing..."
kubectl delete pod "$old_pod" -n "$namespace" --wait=false >/dev/null
new_pod="$(wait_for_replacement "$old_pod")"
echo "Replacement Pod is Ready: $new_pod"

read_response="$(api_request \
  "http://$app_name/get_product_amount?product_name=testproduct")"
if [[ "$read_response" != *'"amount":7'* ]]; then
  echo "data did not survive Pod replacement: $read_response" >&2
  exit 1
fi

echo "Updating the template and waiting for a serial rollout..."
kubectl patch etherealpod "$app_name" -n "$namespace" --type merge \
  -p "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"sunday.system/e2e-rollout\":\"$new_pod\"}}}}}" >/dev/null
new_pod="$(wait_for_replacement "$new_pod")"
kubectl wait etherealpod "$app_name" -n "$namespace" \
  --for=condition=Ready --timeout=30s >/dev/null
read_response="$(api_request \
  "http://$app_name/get_product_amount?product_name=testproduct")"
if [[ "$read_response" != *'"amount":7'* ]]; then
  echo "data did not survive template rollout: $read_response" >&2
  exit 1
fi
restarts="$(kubectl get etherealpod "$app_name" -n "$namespace" \
  -o jsonpath='{.status.restarts}')"
if [[ "$restarts" != 0 ]]; then
  echo "expected an explicit zero restart count, got: $restarts" >&2
  exit 1
fi

echo "Creating a deliberately crashing EtherealPod..."
kubectl apply -f - >/dev/null <<EOF
apiVersion: sunday.system/v1alpha1
kind: EtherealPod
metadata:
  name: $crash_name
  namespace: $namespace
spec:
  template:
    spec:
      containers:
        - name: crash
          image: busybox:1.36
          command: ["sh", "-c", "echo deliberate crash; exit 1"]
EOF

restarts="$(wait_for_restart_count)"
echo "Reported restart count: $restarts"

api_request -X DELETE \
  "http://$app_name/delete_product?product_name=testproduct" >/dev/null

echo "E2E checks passed: API persistence, Pod self-healing, template rollout, and restart reporting."
