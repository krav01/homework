#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$script_dir"

cluster_name="${KIND_CLUSTER_NAME:-sunday-system}"

for command in docker kind kubectl; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "required command is missing: $command" >&2
    exit 1
  fi
done

if ! kind get clusters | grep -Fxq "$cluster_name"; then
  kind create cluster --name "$cluster_name"
fi
kubectl config use-context "kind-$cluster_name" >/dev/null

docker build -f Dockerfile.controller -t sunday-controller:dev .
docker build -f Dockerfile.sundayapp -t sunday-app:dev .
kind load docker-image --name "$cluster_name" sunday-controller:dev sunday-app:dev

kubectl apply -f config/crd/bases/sunday.system_etherealpods.yaml
kubectl wait --for=condition=Established crd/etherealpods.sunday.system --timeout=60s
kubectl apply -f submission.yaml
kubectl rollout restart deployment/etherealpod-controller -n sunday-system
kubectl rollout status deployment/etherealpod-controller -n sunday-system --timeout=120s

# Recreate the application Pod so a repeated setup run uses the image that was
# just loaded into kind, even though the development tag stays unchanged.
old_pod="$(kubectl get etherealpod/sunday-app -n sunday-system \
  -o jsonpath='{.status.podName}' 2>/dev/null || true)"
if [[ -n "$old_pod" ]]; then
  kubectl delete pod "$old_pod" -n sunday-system \
    --ignore-not-found --wait=true >/dev/null
fi

replacement_ready=false
for _ in {1..60}; do
  new_pod="$(kubectl get etherealpod/sunday-app -n sunday-system \
    -o jsonpath='{.status.podName}' 2>/dev/null || true)"
  if [[ -n "$new_pod" && "$new_pod" != "$old_pod" ]] && \
    kubectl wait pod "$new_pod" -n sunday-system \
      --for=condition=Ready --timeout=2s >/dev/null 2>&1; then
    replacement_ready=true
    break
  fi
  sleep 2
done
if [[ "$replacement_ready" != true ]]; then
  echo "SundayApp Pod did not become Ready" >&2
  exit 1
fi
kubectl wait --for=condition=Ready etherealpod/sunday-app -n sunday-system --timeout=120s

cat <<'EOF'
Sunday System is ready.

Inspect the custom resource:
  kubectl get eps -n sunday-system

Open the API locally:
  kubectl port-forward -n sunday-system service/sunday-app 8080:80
EOF
