# TI-ONE 自定义开发机镜像构建经验

> 官方文档：https://cloud.tencent.com/document/product/851/86695
> 适用场景：TI-ONE 平台的 Notebook 开发机 / 任务式建模自定义镜像
>
> 本目录文件：
> - `Dockerfile.cpu` — CPU 基础镜像定义（已验证可用的 v3 版本）
> - `README.md` — 本文档，构建经验与踩坑记录

## 核心原则：不要覆盖平台的启动流程

**最关键的教训**：TI-ONE 平台的开发机容器启动时，**平台会自行注入并启动 sshd、配置 authorized_keys**。镜像里自定义 `CMD`、自己启动 sshd、自己改 `sshd_config` 都会**覆盖或冲突**平台的默认行为，反而导致 SSH 登录失败。

### ❌ 错误做法（会导致 SSH 卡在 publickey 认证）
```dockerfile
# 自定义 sshd 配置 → 覆盖平台注入的配置
RUN echo "PermitRootLogin yes" >> /etc/ssh/sshd_config && \
    echo "PasswordAuthentication yes" >> /etc/ssh/sshd_config

# 自定义启动脚本 → 覆盖平台启动流程
CMD ["/opt/dl/run"]
```

### ✅ 正确做法（遵循官方案例）
- **不要写 `CMD`**
- **不要改 `sshd_config`**
- **不要在 `/opt/dl/run` 里启动 sshd**
- 只需要 `ENTRYPOINT ["/usr/bin/tini", "-g", "--"]` 回收僵尸进程

## 最小可用 Dockerfile 模板（CPU）

```dockerfile
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive

# 腾讯云内网镜像源加速（构建机在腾讯云时）
ENV TENCENT_MIRRORS="mirrors.tencentyun.com"
RUN sed -i "s@archive.ubuntu.com@${TENCENT_MIRRORS}@g" /etc/apt/sources.list.d/ubuntu.sources 2>/dev/null || true && \
    sed -i "s@security.ubuntu.com@${TENCENT_MIRRORS}@g" /etc/apt/sources.list.d/ubuntu.sources 2>/dev/null || true

# [基本镜像规范] openssh-server + git + tini 必装
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        openssh-server git curl wget ca-certificates tini && \
    apt-get clean && rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/run/sshd

# Python 环境
RUN apt-get update && \
    apt-get install -y --no-install-recommends python3 python3-pip && \
    apt-get clean && rm -rf /var/lib/apt/lists/* && \
    ln -sf /usr/bin/python3 /usr/bin/python && \
    ln -sf /usr/bin/pip3    /usr/bin/pip

# Ubuntu 24.04 的 pip 受 PEP 668 限制，必须加 --break-system-packages
RUN pip install --no-cache-dir --break-system-packages jupyterlab

# [开发机镜像规范] /opt/dl/run 启动脚本（只启动 jupyter，不要启动 sshd）
RUN mkdir -p /opt/dl && \
    echo "cd /home/tione/notebook && jupyter lab --allow-root --no-browser --ip=0.0.0.0 --port=8888 --notebook-dir=/home/tione/notebook --NotebookApp.allow_origin='*' --NotebookApp.token=''" > /opt/dl/run && \
    chmod a+x /opt/dl/run

RUN mkdir -p /home/tione/notebook

# 使用 tini 回收僵尸进程
ENTRYPOINT ["/usr/bin/tini", "-g", "--"]
```

## 镜像规范要点

| 规范 | 要求 |
|------|------|
| **SSH** | 必装 `openssh-server`，创建 `/var/run/sshd`，**不要自己启动 sshd** |
| **Git** | 必装 `git`（任务式建模需要） |
| **Jupyter** | 开发机必装 `jupyterlab` |
| **启动脚本** | 固定路径 `/opt/dl/run`，必须可执行，内容只放 jupyter 启动命令 |
| **工作目录** | `/home/tione/notebook`（Jupyter 挂载/工作路径） |
| **端口** | Jupyter `8888`，SSH `22`（不需要 `EXPOSE`） |
| **ENTRYPOINT** | 推荐 `tini` 回收僵尸进程 |

## 常见坑

### 1. Ubuntu 24.04 pip 报 PEP 668
```
error: externally-managed-environment
```
**解决**：`pip install` 时加 `--break-system-packages`。

### 2. SSH 卡住（TCP 通但 SSH 握手超时）
**原因**：平台端口映射建立时，容器内 sshd 未监听 22 端口（镜像里 `CMD` 只启动了 jupyter 没启动 sshd）。
**解决**：删掉 `CMD`，让平台接管启动流程，平台会自己拉起 sshd。

### 3. SSH `Permission denied (publickey)`
**原因**：镜像里自定义了 `sshd_config`（如 `PermitRootLogin yes`），覆盖了平台注入的配置。
**解决**：删掉所有 `sshd_config` 的修改，让平台用自己的默认配置。

### 4. DevCloud 机器推送镜像 `http: server gave HTTP response to HTTPS client`
**原因**：内网镜像仓库只支持 HTTP。
**解决**：`/etc/docker/daemon.json` 的 `insecure-registries` 加上目标域名，然后 `systemctl restart docker`。

### 5. DevCloud 机器推送镜像 `denied: 访问目标IDC环境需要通过igate开通`
**原因**：DevCloud 访问 IDC 内的镜像仓库需 igate 授权。
**解决**：申请 igate 权限（https://iwiki.woa.com/p/50799311），或换一个公网可达的 TCR 实例。

### 6. DNS 解析失败 `no such host`
**原因**：内网 DNS 可能没有目标 TCR 域名的解析记录。
**解决**：`nslookup <domain>` 验证；必要时手动绑 `/etc/hosts`（IP 从 TCR 控制台查）。

## 构建与推送流程

```bash
# 1. 构建
docker build -t <registry>/<namespace>/<image>:<tag> -f Dockerfile.cpu .

# 2. 登录（token 从 TCR 控制台"访问凭证"获取，有效期 1 小时）
echo '<token>' | docker login <registry> --username <uin> --password-stdin

# 3. 推送
docker push <registry>/<namespace>/<image>:<tag>
```

## 验证开发机 SSH 是否就绪

登录开发机的 Web 终端，执行：
```bash
# 确认 sshd 在监听 22
netstat -tlnp | grep :22

# 确认 authorized_keys 已下发
cat /etc/ssh/authorized_keys

# 本地自测 banner
timeout 5 bash -c 'exec 3<>/dev/tcp/127.0.0.1/22; head -1 <&3'
# 正常返回：SSH-2.0-OpenSSH_8.9p1 Ubuntu-3ubuntu0.13
```

注意：平台注入的 sshd 版本是 **OpenSSH 8.9 (Ubuntu 22.04)**，不是镜像里 Ubuntu 24.04 自带的 9.6——再次印证 sshd 由平台接管。

## 调试技巧

- 镜像启动卡住 → 登录 Web 终端看 `ps aux`，确认 sshd / jupyter 进程是否正常
- SSH 端口 TCP 通但握手失败 → 容器内 sshd 未监听；**不要**自己起 sshd 绕过，改镜像走平台流程
- 认证失败 → 先在容器内 `ssh-keygen -lf /etc/ssh/authorized_keys` 核对公钥指纹与本地 `~/.ssh/id_*.pub` 是否一致
