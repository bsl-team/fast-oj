#!/usr/bin/env bash
# 把判机跑进容器：交叉编译 → 构建镜像 → 起容器。
# fast-oj 服务器仍然跑在宿主机上，判机只出站连它。
#
#   FASTOJ_JUDGE_TOKEN=JDG bash scripts/judge.docker.sh
#
set -eu
cd "$(dirname "$0")/.."

GO="${GO:-go}"
ARCH="${ARCH:-amd64}"
SERVER="${FASTOJ_SERVER:-http://host.docker.internal:18080}"
TOKEN="${FASTOJ_JUDGE_TOKEN:?判机令牌，要和服务器启动时的 -judge-token 一致}"

echo "==> 交叉编译 linux/$ARCH"
mkdir -p "dist/linux_$ARCH"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" "$GO" build -o "dist/linux_$ARCH/fast-judge" ./cmd/fast-judge

echo "==> 构建镜像 fast-oj-judge:latest"
# 基础镜像要在 Docker Hub 上取元数据，网络不通时这一步会失败；本地已经有镜像就复用，
# 需要重装工具链时用 REBUILD=1 强制重建。
if [ "${REBUILD:-0}" = "1" ] || ! docker image inspect fast-oj-judge:latest >/dev/null 2>&1; then
  docker build -f Dockerfile.judge -t fast-oj-judge .
else
  echo "    复用已有镜像（REBUILD=1 可强制重建）"
fi

echo "==> 起容器（$SERVER）"
FASTOJ_SERVER="$SERVER" FASTOJ_JUDGE_TOKEN="$TOKEN" \
  docker compose -f docker-compose.judge.yml up -d --no-build

sleep 2
docker logs --tail 20 fast-oj-judge || true
echo
echo "验证：宿主上执行 curl -s -H 'Authorization: Bearer <admin令牌>' <服务器地址>/about | grep counters"
echo "     judges 应该 +1；浏览器打开 /judges 能看到这台容器判机。"
