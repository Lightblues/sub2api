#!/bin/bash
# =============================================================================
# Sub2API TI-ONE entrypoint
# =============================================================================
# 职责:
#   1. 确保 CFS 下目录结构与权限正确
#   2. 首次启动时 initdb + 建库建用户(幂等)
#   3. 首次启动时从模板拷入 config.yaml(幂等)
#   4. 从 CFS /data/sub2api/youtu_proxy/.env 读取敏感凭证并导出
#   5. exec 传入的 CMD(默认为 supervisord)
#
# 幂等原则: 所有 mkdir/cp 都先判断是否已存在,不会覆盖 CFS 上已有数据
# =============================================================================

set -eo pipefail

log() {
    echo "[entrypoint $(date '+%Y-%m-%d %H:%M:%S')] $*"
}

# -----------------------------------------------------------------------------
# 从环境变量读取(允许 TI-ONE 控制台覆盖)
# -----------------------------------------------------------------------------
PG_VERSION="${PG_VERSION:-15}"
PG_BIN="${PG_BIN:-/usr/lib/postgresql/${PG_VERSION}/bin}"
PG_DATA_DIR="${PG_DATA_DIR:-/data/sub2api/pgdata}"
REDIS_DATA_DIR="${REDIS_DATA_DIR:-/data/sub2api/redis}"
SUB2API_DATA_DIR="${SUB2API_DATA_DIR:-/data/sub2api/appdata}"
YOUTU_PROXY_DIR="${YOUTU_PROXY_DIR:-/data/sub2api/youtu_proxy}"
SUB2API_LOG_DIR="${SUB2API_DATA_DIR}/logs"

# PG 内部使用的凭证(sub2api 通过 127.0.0.1 连接)
SUB2API_DB_NAME="${SUB2API_DB_NAME:-sub2api}"
SUB2API_DB_USER="${SUB2API_DB_USER:-sub2api}"
SUB2API_DB_PASSWORD="${SUB2API_DB_PASSWORD:-}"

# 凭证落盘文件(幂等 — 首次生成后一直复用,重启不会变)
DB_PASSWORD_FILE="${SUB2API_DATA_DIR}/.db_password"

# -----------------------------------------------------------------------------
# 1. 目录准备
# -----------------------------------------------------------------------------
log "ensuring CFS directories under /data/sub2api"
mkdir -p \
    "${PG_DATA_DIR}" \
    "${REDIS_DATA_DIR}" \
    "${SUB2API_DATA_DIR}" \
    "${SUB2API_LOG_DIR}" \
    "${YOUTU_PROXY_DIR}"

# PG / Redis 数据目录必须属于对应 user
chown -R postgres:postgres "${PG_DATA_DIR}"
chmod 700 "${PG_DATA_DIR}"
chown -R redis:redis "${REDIS_DATA_DIR}" 2>/dev/null || true

# sub2api 以 root 跑(方便写 CFS),日志目录保证可写
# logs 目录需要所有子进程都能写入(postgres / redis / youtu_proxy / sub2api)
chmod 777 "${SUB2API_LOG_DIR}"
chmod -R u+rwX "${SUB2API_DATA_DIR}"

# -----------------------------------------------------------------------------
# 2. 生成/加载 DB 密码(幂等)
# -----------------------------------------------------------------------------
if [ -z "${SUB2API_DB_PASSWORD}" ]; then
    if [ -f "${DB_PASSWORD_FILE}" ]; then
        SUB2API_DB_PASSWORD="$(cat "${DB_PASSWORD_FILE}")"
        log "loaded DB password from ${DB_PASSWORD_FILE}"
    else
        SUB2API_DB_PASSWORD="$(openssl rand -hex 24)"
        umask 077
        echo -n "${SUB2API_DB_PASSWORD}" > "${DB_PASSWORD_FILE}"
        log "generated new DB password -> ${DB_PASSWORD_FILE}"
    fi
fi
export SUB2API_DB_PASSWORD

# -----------------------------------------------------------------------------
# 3. PostgreSQL 初始化(幂等)
# -----------------------------------------------------------------------------
if [ ! -s "${PG_DATA_DIR}/PG_VERSION" ]; then
    log "first boot: initdb at ${PG_DATA_DIR}"
    su - postgres -c "${PG_BIN}/initdb \
        -D ${PG_DATA_DIR} \
        --encoding=UTF8 \
        --locale=C.UTF-8 \
        --auth-local=trust \
        --auth-host=md5"

    # 只监听本地(sub2api 和 PG 在同容器)
    cat >> "${PG_DATA_DIR}/postgresql.conf" <<EOF

# -- sub2api tione overrides --
listen_addresses = '127.0.0.1'
port = 5432
unix_socket_directories = '/var/run/postgresql, ${PG_DATA_DIR}'
logging_collector = on
log_directory = '${SUB2API_LOG_DIR}'
log_filename = 'postgres-%Y-%m-%d.log'
log_rotation_age = 1d
log_min_duration_statement = 500
EOF

    # 只允许本机 md5
    cat > "${PG_DATA_DIR}/pg_hba.conf" <<EOF
local   all             postgres                                trust
local   all             all                                     trust
host    all             all             127.0.0.1/32            md5
host    all             all             ::1/128                 md5
EOF

    # 临时启动 PG 以便建库建用户
    log "bootstrapping database ${SUB2API_DB_NAME} and user ${SUB2API_DB_USER}"
    su - postgres -c "${PG_BIN}/pg_ctl -D ${PG_DATA_DIR} -w -t 60 -l ${SUB2API_LOG_DIR}/pg-bootstrap.log start"
    su - postgres -c "psql -v ON_ERROR_STOP=1 <<SQL
CREATE USER ${SUB2API_DB_USER} WITH PASSWORD '${SUB2API_DB_PASSWORD}';
CREATE DATABASE ${SUB2API_DB_NAME} OWNER ${SUB2API_DB_USER};
GRANT ALL PRIVILEGES ON DATABASE ${SUB2API_DB_NAME} TO ${SUB2API_DB_USER};
SQL"
    su - postgres -c "${PG_BIN}/pg_ctl -D ${PG_DATA_DIR} -w -t 60 stop"
    log "PG bootstrap done"
else
    log "PG_VERSION exists, skipping initdb"
    # 密码可能被 rotate 了(用户主动改 .db_password),重建 role 密码
    # 仅当文件比 PG_VERSION 新时才同步
    if [ -f "${DB_PASSWORD_FILE}" ] && [ "${DB_PASSWORD_FILE}" -nt "${PG_DATA_DIR}/PG_VERSION" ]; then
        log "rotating DB user password (db password file updated)"
        su - postgres -c "${PG_BIN}/pg_ctl -D ${PG_DATA_DIR} -w -t 60 -l ${SUB2API_LOG_DIR}/pg-rotate.log start"
        su - postgres -c "psql -v ON_ERROR_STOP=1 -c \"ALTER USER ${SUB2API_DB_USER} WITH PASSWORD '${SUB2API_DB_PASSWORD}';\""
        su - postgres -c "${PG_BIN}/pg_ctl -D ${PG_DATA_DIR} -w -t 60 stop"
    fi
fi

# -----------------------------------------------------------------------------
# 4. sub2api 配置文件(幂等,首次拷模板)
# -----------------------------------------------------------------------------
SUB2API_CONFIG="${SUB2API_DATA_DIR}/config.yaml"
if [ ! -f "${SUB2API_CONFIG}" ]; then
    log "first boot: copying default config to ${SUB2API_CONFIG}"
    cp /opt/sub2api/config.default.yaml "${SUB2API_CONFIG}"
fi

# -----------------------------------------------------------------------------
# 5. 加载 youtu_proxy 凭证(CFS .env)
# -----------------------------------------------------------------------------
YOUTU_ENV="${YOUTU_PROXY_DIR}/.env"
if [ ! -f "${YOUTU_ENV}" ]; then
    log "no youtu .env found, seeding from template -> ${YOUTU_ENV}"
    cp /opt/youtu_proxy/.env.example "${YOUTU_ENV}"
    log "⚠️  please edit ${YOUTU_ENV} with real YOUTU_LLM_TOKEN/USERID/USERNAME"
fi

# 把 .env 中的非注释行 export 到当前环境,供 supervisord 的 %(ENV_XXX)s 引用
set -a
# shellcheck disable=SC1090
. "${YOUTU_ENV}" || log "warning: failed to source ${YOUTU_ENV}"
set +a

# -----------------------------------------------------------------------------
# 6. 导出 sub2api 需要的环境变量
#    (sub2api 读取 ENV 优先于 config.yaml)
# -----------------------------------------------------------------------------
export DATABASE_HOST="${DATABASE_HOST:-127.0.0.1}"
export DATABASE_PORT="${DATABASE_PORT:-5432}"
export DATABASE_USER="${DATABASE_USER:-${SUB2API_DB_USER}}"
export DATABASE_PASSWORD="${DATABASE_PASSWORD:-${SUB2API_DB_PASSWORD}}"
export DATABASE_DBNAME="${DATABASE_DBNAME:-${SUB2API_DB_NAME}}"
export DATABASE_SSLMODE="${DATABASE_SSLMODE:-disable}"
export REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
export REDIS_PORT="${REDIS_PORT:-6379}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-}"
export SERVER_HOST="${SERVER_HOST:-0.0.0.0}"
export SERVER_PORT="${SERVER_PORT:-8080}"
export SERVER_MODE="${SERVER_MODE:-release}"
export RUN_MODE="${RUN_MODE:-standard}"
# sub2api 读取配置文件的位置约定为 /app/data/config.yaml(参考 docker-compose)
mkdir -p /app/data
ln -sfn "${SUB2API_CONFIG}" /app/data/config.yaml

# 导出给 supervisord 使用(子进程继承)
export YOUTU_LLM_BASE="${YOUTU_LLM_BASE:-http://112.65.194.90:8001}"
export YOUTU_LLM_TOKEN="${YOUTU_LLM_TOKEN:-}"
export YOUTU_LLM_USERID="${YOUTU_LLM_USERID:-}"
export YOUTU_LLM_USERNAME="${YOUTU_LLM_USERNAME:-}"
export YOUTU_LLM_PROXY_PORT="${YOUTU_LLM_PROXY_PORT:-8088}"

log "env ready: sub2api :${SERVER_PORT}, youtu_proxy :${YOUTU_LLM_PROXY_PORT}, pg :5432, redis :6379"

# -----------------------------------------------------------------------------
# 7. 交给 supervisord(或用户传入的 CMD)
# -----------------------------------------------------------------------------
log "exec: $*"
exec "$@"
