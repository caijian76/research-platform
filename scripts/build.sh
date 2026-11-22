#!/usr/bin/env bash
set -euo pipefail

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-off}"
export GOMODCACHE="${GOMODCACHE:-/root/go/pkg/mod}"
export TMPDIR="${TMPDIR:-/var/tmp}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
CTX="$DIST/backend-context"
WORKSPACE_IMAGE="registry.cn-hangzhou.aliyuncs.com/jackc/ubuntu-24-allinone:v0.4.0"
mkdir -p "$DIST/images" "$CTX"

echo "==> building frontend"
cd "$ROOT/frontend"
if [ -f package-lock.json ]; then
  npm ci --no-audit --no-fund
else
  npm install --no-audit --no-fund
fi
npm run build

echo "==> building backend"
cd "$ROOT/backend"
CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o "$DIST/research-platform-server" ./cmd/server

echo "==> preparing backend image context"
cp -f "$DIST/research-platform-server" "$CTX/research-platform-server"
rm -rf "$CTX/web"
cp -a "$ROOT/backend/web" "$CTX/web"

echo "==> building backend image"
podman build -t research-platform-backend:1.0.0 -f "$ROOT/images/backend/Containerfile" "$CTX"

echo "==> fetching images"
podman pull "$WORKSPACE_IMAGE"
podman pull docker.1ms.run/library/postgres:16-alpine

echo "==> importing images into k8s containerd"
# Import one image per archive: this podman version collapses a multi-image
# docker-archive into a single manifest whose RepoTags all point at one image,
# which silently corrupts unrelated tags in containerd.
for img in research-platform-backend:1.0.0 "$WORKSPACE_IMAGE" docker.1ms.run/library/postgres:16-alpine; do
  file="$DIST/images/$(echo "$img" | tr '/:@' '___').tar"
  \rm -f "$file"
  podman save --format docker-archive -o "$file" "$img" >/dev/null
  ctr -n k8s.io images import "$file"
  \rm -f "$file"
done
ctr -n k8s.io images tag --force localhost/research-platform-backend:1.0.0 docker.io/library/research-platform-backend:1.0.0
crictl images | grep -E 'research-platform-backend|ubuntu-24-allinone|postgres' || true

echo "==> images ready"