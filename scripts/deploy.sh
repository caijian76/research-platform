#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "==> applying manifests"
kubectl apply -f "$ROOT/deploy/00-namespace-rbac.yaml"
kubectl apply -f "$ROOT/deploy/01-postgres.yaml"
kubectl apply -f "$ROOT/deploy/02-secrets-config.yaml"

NODE_IP="$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')"
if [ -n "$NODE_IP" ]; then
  echo "==> public host: $NODE_IP"
  kubectl -n research-system patch configmap research-platform-config --type merge \
    -p "{\"data\":{\"PUBLIC_HOST\":\"$NODE_IP\"}}"
fi

kubectl apply -f "$ROOT/deploy/03-backend.yaml"
kubectl -n research-system rollout restart deployment/research-platform
kubectl -n research-system rollout status deployment/postgres --timeout=300s
kubectl -n research-system rollout status deployment/research-platform --timeout=300s

echo
kubectl -n research-system get pods,svc
echo
echo "平台访问地址: http://${NODE_IP:-<node-ip>}:30080"
echo "默认平台管理员: admin / Admin@12345 （请登录后立即修改，并同步更新 Secret）"