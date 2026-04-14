# 运维日志

## 2026-03-13：初次部署（devcloud_ubuntu）

**部署信息**
- 机器：`ssh devcloud_ubuntu`，路径：`/root/serve/sub2api/`
- 镜像：`weishaw/sub2api:0.1.96`，模式：`standard`
- 端口：`0.0.0.0:8066`（无反向代理）
- 管理员：`oldcitystal@gmail.com`

### 踩坑

**镜像 tag 无 `v` 前缀**
- ❌ `weishaw/sub2api:v0.1.96`
- ✅ `weishaw/sub2api:0.1.96`

**账号绑定 Group 用 `group_ids`（数组）不是 `group_id`**
- Account ↔ Group 是多对多关系
- `PUT /api/v1/admin/accounts/:id` 传 `{"group_ids": [4]}` → 正确
- 传 `{"group_id": 4}` → 静默成功但无效

**Account platform 必须和 Group platform 匹配**
- OpenAI OAuth 账号 → 只能绑到 OpenAI 类型的 Group
- Antigravity 账号 → 只能绑到 Antigravity 类型的 Group

**standard 模式下用户余额初始为 0**
- 新用户 `balance: 0`，所有请求会被拦截
- 充值：`POST /api/v1/admin/users/:id/balance`，body `{"balance": N, "operation": "add"}`
- `PUT /api/v1/admin/users/:id` 直接设 balance 有上限（约 100），不能用来大额充值

**用户的 `allowed_groups` 必须显式设置**
- 新用户 `allowed_groups: null`，无法使用任何 Group
- `PUT /api/v1/admin/users/:id` 传 `{"allowed_groups": [4], "concurrency": 10}`

**权限粒度只有 admin / user**
- 无 per-group 委托权限
- admin 看全部，user 只看自己的 key 和用量

---

## 2026-03-13：请求日志 + anydev_llmrouter 部署

**部署信息**
- 机器：`ssh anydev_llmrouter`（`sn5llmrouter.devcloud.woa.com`）
- 路径：`/root/sub2api/`（docker-compose.yml、.env、config.yaml、src/）
- 镜像：`sub2api:eason`（自定义构建，来自 `EasonAgent/sub2api` branch `eason`）
- 端口：`0.0.0.0:80`
- DB/Redis：完整栈（从 devcloud_ubuntu 导入数据）
- 磁盘：`/data` 98G，约 75G 可用

### 踩坑

**Passthrough vs Non-passthrough：两条独立的流式路径**
- `forwardOpenAIPassthrough`：仅当 `account.IsOpenAIPassthroughEnabled()` 为 true 时使用
- `Forward` → `handleStreamingResponse`：其他所有账号（大多数 Codex 流量）
- 初版只给 passthrough 加了日志，Codex 请求悄悄走了 non-passthrough，无 JSONL 写出
- 现象：请求 200 成功，但没有日志

**Codex 走 `/responses` 不是 `/v1/responses`**
- sub2api 同时注册了 `/v1/responses` 和根路径别名 `/responses`
- 两个路由指向同一 handler，不是 bug

**SCP 在 devcloud 机器上报 "Received message too long"**
- SSH banner（`WARNING: connection is not using a post-quantum...`）破坏 SCP 协议
- 替代方案：`cat file | ssh host "cat > /path"`

**Docker `/data` 磁盘满**
- 98G `/data` 曾被旧镜像（verl:v0.4.0 = 30G 等）占满
- `docker system prune -af --volumes` 释放大量空间
- 需定期监控磁盘

**在无本地 Docker 的情况下构建镜像**
- fork 仓库到 GitHub org（`gh repo fork --org EasonAgent`）
- 在远端机器 clone，直接 `docker build`
- 后续重建：`git pull && docker build && docker compose up -d`

---

## 2026-03-20：批量导入 OpenAI 账号（Playwright）

**背景**：导入 8 个 OpenAI 账号，原有 refresh token 全部过期。

**方案**：CC Agent + Playwright 插件（逐账号自动化登录 + 读取邮件验证码）

**流程（每个账号）**：
1. Python 生成 PKCE 参数 + OAuth URL（`openai_oauth_helper.py gen-url`）
2. Playwright 打开 `auth.openai.com`，填邮箱 → 密码 → 到达邮箱验证页
3. Playwright 新 Tab 打开 Outlook（带 `?login_hint=EMAIL`），从垃圾邮件读 6 位验证码
4. 填验证码 → 同意页（选 MyTeam）→ 重定向 localhost
5. 从 `performance.getEntriesByType('navigation')` 提取 auth code
6. Python 换取 token（`openai_oauth_helper.py exchange`）
7. Python 在 sub2api 创建账号（`openai_oauth_helper.py add-account`）

### 关键发现

**OpenAI OAuth**：
- 不走密码登录，始终走邮箱 OTP
- 验证码发到账号邮箱，有效期约 10 分钟
- OTP 后出现 Codex 同意页，必须选 **MyTeam**（不是 Personal）
- 重定向到 `http://localhost:1455/auth/callback`，浏览器报 ERR_CONNECTION_REFUSED 是预期现象
- 从报错页提取 code：`performance.getEntriesByType('navigation')[0].name`

**Outlook**：
- 验证邮件在**垃圾邮件/Junk 文件夹**
- `?login_hint=EMAIL` 自动切换到对应账号，无需重新登录
- 常见打断：「保护账号」页面 → 跳过（可能需点两次）、Passkey 提示 → 取消、「保持登录？」→ Yes

**Playwright 技巧**：
- OpenAI 邮件输入框需用 `pressSequentially`（不是 `fill`）才能触发 JS 验证
- `waitForURL('**/pattern')` 比 `waitForTimeout` 更稳定
- 批量操作封装在单次 `run_code` 调用中更快更稳定

**sub2api 导入 API**：
- `POST /api/v1/admin/openai/refresh-token`：先验 RT 是否有效
- `POST /api/v1/admin/accounts`：`type: "oauth"`，`credentials: {access_token, refresh_token, id_token, client_id, email}`，`group_ids: [4]`
- `POST /api/v1/admin/openai/accounts/:id/refresh`：验证账号 token refresh 正常

### 当前账号状态（8 个账号）

| ID | 邮箱 | 平台 | 状态 |
|----|------|------|------|
| 94 | BrandiZiemesb@outlook.com | openai | active |
| 95 | LavonneKutchawr@outlook.com | openai | active |
| 96 | EveKrajcikepvl@outlook.com | openai | active |
| 97 | MelvinMitchelliurwf@outlook.com | openai | active |
| 98 | LucasHellerwcyh@outlook.com | openai | active |
| 99 | LawsonMarksst@outlook.com | openai | active |
| 103 | MikelKerlukerv@outlook.com | openai | active |
| 104 | LuzKochreweg@outlook.com | openai | active |

### 当前用户状态

| 用户 | ID | Groups | 余额 |
|------|----|--------|------|
| oldcitystal@gmail.com（admin） | 1 | openai、eason-antigravity | ~$1M |
| ianxxu@tencent.com | 2 | openai | $1M |
| tristanli@tencent.com | 3 | openai | $1M |
| caffei@tencent.com | 4 | openai | $1M |
| siqiicai@tencent.com | 5 | openai | ~$1M |
| maoyongmao@tencent.com | 6 | openai | $1M |

| Group | ID | 平台 | 账号数 |
|-------|----|------|--------|
| openai | 4 | openai | 8 个 OAuth 账号 |
| eason-antigravity | — | antigravity | 1 个 OAuth 账号 |
| default | 1 | anthropic | 空 |

---

## 2026-04-03：磁盘满导致服务无法登录

**现象**：`Too many requests, please try again later`（实为 DB 连接失败）

**根因**：`/data`（98G）100% 满
- 约 43 个 `harbor-terminal-bench` 镜像在 1-2 小时内被自动拉入，每个 4-10GB
- PostgreSQL 无法写 `postmaster.pid` → 容器不断 crash-restart
- sub2api 连不上 DB → 登录失败

**修复**：
1. `docker system prune -f` 释放 28GB（stopped 容器 + dangling 镜像）
2. `docker rmi -f` 删除全部 terminal-bench 镜像，`/data` 从 100% 降至 32%
3. `docker compose up -d sub2api-postgres` 重启 PG
4. `docker compose restart sub2api` 重连 DB

**磁盘现状**（修复后）：
- `/data`：98G 总计，32% 使用，64G 可用
- SQLite 请求日志：**~21GB**（18GB 主库 + 2.6GB WAL），是最大增长来源

**待办**：将 SQLite 日志迁移到 `/mnt/private/`（2TB ceph，当前用量 56G/2T）
- PostgreSQL 留在 `/data`（ceph-fuse fsync 语义不可靠，不适合数据库）
- 方案：修改 docker-compose.yml，将 `sub2api_data` volume bind mount 到 `/mnt/private/sub2api/data`

---

## 2026-04-08：升级到 upstream v0.1.110

**变更**：
- 合并 upstream/main（218 commits），解决 7 个冲突（均在 request_log 和 branding 自定义代码中）
- 新增上游功能：Channel 管理、TLS 指纹模板管理、BufferedResponseAccumulator、billingModel/upstreamModel 拆分
- 上游移除了 Sora 功能
- 修复 `handlePassthroughSSEToJSON` 返回值以兼容自定义 request_log

**部署步骤**：
```bash
cd /root/sub2api/src && git pull origin eason
docker build -t sub2api:eason .
cd /root/sub2api && docker compose up -d sub2api
```

**HTTPS 问题诊断**：
- `http://` 和 `https://sn5llmrouter.devcloud.woa.com` 均可正常访问（包括 API 端点）
- HTTPS 由 AIO-Forward 代理终止 TLS（DigiCert 通配证书 `*.devcloud.woa.com`）
- Claude Code 使用 HTTPS 时可能遇到的问题：Node.js 不走系统代理、AIO-Forward 对长连接 SSE 流有超时/缓冲
- **建议**：使用 `http://` 直连，绕过 AIO-Forward 的 TLS 层
