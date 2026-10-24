#!/usr/bin/env bash
# 把判机跑进容器：交叉编译 → 构建镜像 → 起容器。
# fast-oj 服务器仍然跑在宿主机上，判机只出站连它。
#
#   FASTOJ_JUDGE_TOKEN=JDG bash scripts/judge.docker.sh
#
# 本机没有 Go 的时候换一份 Dockerfile，把编译挪进容器（只需要一次网络取模块）：
#
#   SOURCE=1 FASTOJ_JUDGE_TOKEN=JDG bash scripts/judge.docker.sh
#
set -eu
cd "$(dirname "$0")/.."

GO="${GO:-go}"
ARCH="${ARCH:-amd64}"
SERVER="${FASTOJ_SERVER:-http://host.docker.internal:18080}"
TOKEN="${FASTOJ_JUDGE_TOKEN:?判机令牌，要和服务器启动时的 -judge-token 一致}"
# SOURCE=1：镜像里编。代价是构建阶段要取模块、要多一个 golang 层；好处是这台机器
# 不需要 Go 工具链，也不需要先手工交叉编译出 dist/linux_amd64/。
SOURCE="${SOURCE:-0}"
DOCKERFILE="Dockerfile.judge"
BINARY="dist/linux_$ARCH/fast-judge"
if [ "$SOURCE" = "1" ]; then
  DOCKERFILE="Dockerfile.judge.source"
  echo "==> 编译交给镜像里的 golang 阶段（本机不跑 go build）"
else
  echo "==> 交叉编译 linux/$ARCH"
  mkdir -p "dist/linux_$ARCH"
  CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" "$GO" build -o "$BINARY" ./cmd/fast-judge
fi

echo "==> 构建镜像 fast-oj-judge:latest"
# 基础镜像要在 Docker Hub 上取元数据，网络不通时这一步会失败；本地已经有镜像就复用，
# 需要重装工具链时用 REBUILD=1 强制重建。
if [ "${REBUILD:-0}" = "1" ] || ! docker image inspect fast-oj-judge:latest >/dev/null 2>&1; then
  if ! docker build -f "$DOCKERFILE" -t fast-oj-judge .; then
    # Docker Hub 不可达时 FROM 连元数据都取不到，整镜像重建就会失败。二进制只是被
    # COPY 进去的一层，用本地已有的镜像换个文件提交，等价于只重跑那一层。
    # 这条路只适用于"二进制在本机编好了"的那种 Dockerfile：SOURCE=1 时它不存在。
    if [ "$SOURCE" != "1" ] && docker image inspect fast-oj-judge:latest >/dev/null 2>&1; then
      echo "    构建失败，改用离线方式换掉镜像里的 fast-judge 二进制"
      docker rm -f fast-oj-judge-refresh >/dev/null 2>&1
      # cp 要求容器在跑，所以起一个只会睡觉的临时容器，拷完提交再删掉。
      docker run -d --name fast-oj-judge-refresh --entrypoint sleep         fast-oj-judge:latest 60 >/dev/null
      docker cp "$BINARY" fast-oj-judge-refresh:/usr/local/bin/fast-judge
      # commit 会把临时容器的 --entrypoint 一起记进配置，所以显式写回去。
      docker commit --change 'ENTRYPOINT ["/usr/local/bin/fast-judge"]'         --change 'CMD ["-server", "http://host.docker.internal:8080"]'         fast-oj-judge-refresh fast-oj-judge:latest >/dev/null
      docker rm -f fast-oj-judge-refresh >/dev/null
    else
      exit 1
    fi
  fi
else
  echo "    复用已有镜像（REBUILD=1 可强制重建）"
fi

echo "==> 起容器（$SERVER）"
FASTOJ_SERVER="$SERVER" FASTOJ_JUDGE_TOKEN="$TOKEN" DOCKERFILE="$DOCKERFILE" \
  docker compose -f docker-compose.judge.yml up -d --no-build

sleep 2
docker logs --tail 20 fast-oj-judge || true
echo
echo "验证：宿主上执行 curl -s -H 'Authorization: Bearer <admin令牌>' <服务器地址>/about | grep counters"
echo "     judges 应该 +1；浏览器打开 /judges 能看到这台容器判机。"
