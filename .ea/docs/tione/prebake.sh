#!/bin/bash
# =============================================================================
# Sub2API TI-ONE 预烘脚本 (prebake)
# =============================================================================
# 在开发机 (ssh tione_llmrouter) 上运行,一键完成:
#   1. 构建镜像
#   2. 本地起一次容器,挂假 CFS 目录,让你完成 Setup Wizard
#   3. 把烘好的 CFS 目录上传到真实 CFS 路径
#   4. 推镜像到腾讯云 CR
#
# 用法:
#   cd /root/sub2api
#   ./tione/prebake.sh build       # 只构建镜像
#   ./tione/prebake.sh run         # 起本地容器让你配置
#   ./tione/prebake.sh publish     # 停本地 + 同步 CFS + push 镜像
#   ./tione/prebake.sh all         # 以上三步连做(中间会等你按回车确认 Setup 完成)
# =============================================================================

set -euo pipefail

# -----------------------------------------------------------------------------
# 可配置项 (从环境变量读取,有默认值)
# -----------------------------------------------------------------------------
IMAGE_NAME="${IMAGE_NAME:-zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base}"
IMAGE_TAG="${IMAGE_TAG:-sub2api-v1}"
FULL_IMAGE="${IMAGE_NAME}:${IMAGE_TAG}"

# 本地假 CFS 路径 (prebake 期间用,Setup Wizard 写入的数据会落到这里)
PREBAKE_DIR="${PREBAKE_DIR:-/tmp/sub2api-prebake}"

# 真实 CFS 路径 (publish 阶段会 rsync 过去)
# 开发机上应当已经把 /cfs_turbo/easonsshi/ 挂好
CFS_TARGET="${CFS_TARGET:-/cfs_turbo/easonsshi/sub2api-data}"

# 本地容器名
CONTAINER_NAME="${CONTAINER_NAME:-sub2api-prebake}"

# 本地测试端口映射
HOST_PORT="${HOST_PORT:-18080}"

# -----------------------------------------------------------------------------
# 构建上下文必须是 /root/sub2api (即含 src/ 和 tione/ 的目录)
# -----------------------------------------------------------------------------
CTX_DIR="$(cd "$(dirname "$0")/.." && pwd)"

log() { echo "[prebake $(date '+%H:%M:%S')] $*"; }
die() { echo "[prebake ERROR] $*" >&2; exit 1; }

[ -d "${CTX_DIR}/src" ]   || die "expected ${CTX_DIR}/src to exist"
[ -d "${CTX_DIR}/tione" ] || die "expected ${CTX_DIR}/tione to exist"

# -----------------------------------------------------------------------------
# 子命令
# -----------------------------------------------------------------------------
cmd_build() {
    log "building ${FULL_IMAGE} from ${CTX_DIR}"
    cd "${CTX_DIR}"
    docker build \
        --network=host \
        -f tione/Dockerfile.tione \
        -t "${FULL_IMAGE}" \
        --build-arg VERSION="$(git -C src describe --tags --always 2>/dev/null || echo tione)" \
        --build-arg COMMIT="$(git -C src rev-parse --short HEAD 2>/dev/null || echo tione)" \
        --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        .
    log "build ok -> ${FULL_IMAGE}"
}

cmd_run() {
    log "preparing prebake dir: ${PREBAKE_DIR}"
    mkdir -p "${PREBAKE_DIR}"

    # 若容器已存在先清掉
    if docker ps -a --format '{{.Names}}' | grep -qx "${CONTAINER_NAME}"; then
        log "removing old container ${CONTAINER_NAME}"
        docker rm -f "${CONTAINER_NAME}" >/dev/null
    fi

    log "starting container on host port ${HOST_PORT}"
    docker run -d \
        --name "${CONTAINER_NAME}" \
        -v "${PREBAKE_DIR}:/data/sub2api" \
        -p "${HOST_PORT}:8080" \
        -e TZ=Asia/Shanghai \
        "${FULL_IMAGE}"

    log "container started. tailing last few lines..."
    sleep 3
    docker logs --tail 30 "${CONTAINER_NAME}" || true

    cat <<EOF

=================================================================
  Prebake 容器已启动。请打开浏览器完成配置:

    http://<开发机IP>:${HOST_PORT}

  步骤:
    1. 完成 Setup Wizard,创建 admin 账号
    2. 添加一个 Upstream 类型的账号:
         base_url  = http://127.0.0.1:8088
         api_key   = dummy  (或任意字符串)
    3. 按需配置 groups / model_mapping
    4. 编辑 ${PREBAKE_DIR}/youtu_proxy/.env 填入真实 YOUTU_LLM_TOKEN 等
    5. 验证 /v1/messages 能通

  验证完成后运行:
    $0 publish

  查看日志:
    docker logs -f ${CONTAINER_NAME}
    tail -f ${PREBAKE_DIR}/sub2api/logs/sub2api.out.log
=================================================================
EOF
}

cmd_publish() {
    # 停止本地容器(不删,方便回退)
    if docker ps --format '{{.Names}}' | grep -qx "${CONTAINER_NAME}"; then
        log "stopping ${CONTAINER_NAME}"
        docker stop "${CONTAINER_NAME}" >/dev/null
    fi

    [ -d "${PREBAKE_DIR}" ] || die "prebake dir not found: ${PREBAKE_DIR}. run '$0 run' first."

    # 同步到 CFS
    log "syncing ${PREBAKE_DIR}/ -> ${CFS_TARGET}/"
    mkdir -p "${CFS_TARGET}"
    # -a 保持属主/权限;--delete 可选,默认不删,以免误删 CFS 已有数据
    rsync -a --info=stats2 "${PREBAKE_DIR}/" "${CFS_TARGET}/"

    log "CFS sync done. contents:"
    ls -la "${CFS_TARGET}/" || true

    # 推镜像
    log "pushing image ${FULL_IMAGE}"
    docker push "${FULL_IMAGE}"

    cat <<EOF

=================================================================
  ✅ Prebake 完成

  镜像:     ${FULL_IMAGE}
  CFS 路径: ${CFS_TARGET}

  接下来在 TI-ONE 控制台新建服务:
    - 镜像:      ${FULL_IMAGE}
    - 容器端口:  8080
    - CFS 挂载:  ${CFS_TARGET}  ->  /data/sub2api
    - 健康检查:  POST /health   (若平台强制 POST-only)
                 GET  /health   (若平台允许 GET)
=================================================================
EOF
}

cmd_all() {
    cmd_build
    cmd_run
    echo ""
    read -rp "Setup Wizard 已完成,YOUTU .env 已填好了吗? (按回车继续 publish) "
    cmd_publish
}

cmd_clean() {
    log "removing container ${CONTAINER_NAME}"
    docker rm -f "${CONTAINER_NAME}" 2>/dev/null || true
    log "prebake dir ${PREBAKE_DIR} kept (delete manually if needed)"
}

# -----------------------------------------------------------------------------
# 入口
# -----------------------------------------------------------------------------
case "${1:-}" in
    build)   cmd_build ;;
    run)     cmd_run ;;
    publish) cmd_publish ;;
    all)     cmd_all ;;
    clean)   cmd_clean ;;
    *)
        cat <<EOF
Usage: $0 {build|run|publish|all|clean}

  build    docker build 镜像
  run      起本地容器让你完成 Setup Wizard
  publish  停本地容器 + rsync 到 CFS + push 镜像
  all      build + run + (等你确认) + publish
  clean    删除本地容器

Env vars (可选覆盖):
  IMAGE_NAME   默认 ${IMAGE_NAME}
  IMAGE_TAG    默认 ${IMAGE_TAG}
  PREBAKE_DIR  默认 ${PREBAKE_DIR}
  CFS_TARGET   默认 ${CFS_TARGET}
  HOST_PORT    默认 ${HOST_PORT}
EOF
        exit 1
        ;;
esac
