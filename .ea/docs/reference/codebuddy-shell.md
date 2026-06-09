# CodeBuddy Shell 启动机制与 zplug 兼容性 Bug 记录

> 记录时间: 2026-05-31
> 现象环境: Linux + `/usr/bin/zsh` + zplug + CodeBuddy IDE（agent shell 工具）

## 1. 结论速览（TL;DR）

CodeBuddy 的 agent shell 工具，每次执行命令都是**全新的 zsh 进程**，
它通过把状态 dump 到 `/tmp/zsh-state-{in,out}-*.txt`、下次进程启动时 `eval` 回来，
来伪装成"持续会话"。

**Bug 根因**：dump 函数用
`${(k)parameters[(R)*export*]}` 枚举所有带 export 标志的变量，
然后统一用 `printf "export %s='%s'\n"` 写出。
这套写法对 **array-tied-special 变量**（`path` / `fpath`）和
**associative array**（`_zplug_tags` 等）都是错的：

- `path` / `fpath` 是和 `PATH` / `FPATH` 双向绑定的数组，
  不能用 `export name='string'` 这种 scalar 形式赋值
  → eval 时报 `(eval):export:N: path: inconsistent type for assignment`。
- `_zplug_tags` 等关联数组用 scalar 写出后，
  内部值里的 `\'` 等字符会被错误转义
  → eval 时报 `(eval):N: bad pattern: ...`。

而 zplug 在 `/root/.zplug/base/core/core.zsh` 中执行了：

```zsh
typeset -gx -U path
typeset -gx -U fpath
```

并把一批内部数组/关联数组也标记为 export，
正好踩中 dump 函数的这两个雷区。

**用户侧规避**：在 `~/.zshrc` 末尾给这批变量统一关掉 export 标志（`typeset +x ...`），
让 dump 函数枚举不到它们即可。zplug 自身功能与 PATH/FPATH 同步均不受影响。

## 2. CodeBuddy 怎么启动 shell

每次工具调用大致是这样：

```
/bin/zsh -o extendedglob -c "<WRAPPER>" "zsh" "<USER_CMD>"
```

- `-o extendedglob`：开启扩展通配
- `-c "<WRAPPER>"`：内嵌 wrapper 脚本作为命令
- `"zsh"`：`-c` 模式下的 `$0`
- `"<USER_CMD>"`：用户实际命令，作为 `$1`

特征：**非交互、非登录**，但 wrapper 内部会显式 `source ~/.zshrc`，
所以你交互终端用的 `.zshrc` 配置（包括 zplug）这边照常加载——
**两边是同一个 `/usr/bin/zsh`，同一份 `.zshrc`**。

### 2.1 wrapper 主流程（简化版）

```zsh
dump_zsh_state() {
    # (1) cwd
    printf "cd '%s' 2>/dev/null || true\n" "$(pwd)"

    # (2) ⭐ 枚举所有 export 变量,scalar 形式写出
    for varname in ${(k)parameters[(R)*export*]}; do
        case "$varname" in
            _|SHLVL|PWD|VSCODE_*|ELECTRON_*|NODE_OPTIONS|FZF_*|ATUIN_*) ;;
            *)  local val="${(P)varname}"
                printf "export %s='%s'\n" "$varname" "${val//\'/\'\\\'\'}" ;;
        esac
    done

    # (3) setopt (4) alias -L
    ...
}

snap=$(cat /tmp/zsh-state-in-XXXX.txt)
builtin eval "$snap"                       # ← 报错点：eval 上次坏快照
[ -f ~/.zshrc ] && source ~/.zshrc         # ← 这里触发 zplug 加载
builtin eval "$1"                          # 跑用户命令
dump_zsh_state > /tmp/zsh-state-out-XXXX.txt
exit $?
```

### 2.2 流程图

```
IDE 派生 zsh
    │
    ▼
zsh -o extendedglob -c <WRAPPER> zsh "<USER_CMD>"
    │
    ├─ 定义 dump_zsh_state
    ├─ snap = cat /tmp/zsh-state-in-XXX.txt    ← 上次快照
    ├─ eval "$snap"                            ← ⚠️ 坏快照在此报错
    ├─ unset NODE_OPTIONS ELECTRON_RUN_AS_NODE …
    ├─ source ~/.zshrc                         ← 加载 zplug
    ├─ eval "$1"                               ← 用户命令
    ├─ dump_zsh_state > /tmp/zsh-state-out-XXX ← 写新快照
    └─ exit
```

### 2.3 自取脚本

抓最新 wrapper 内容：

```bash
for pid in $(pgrep -x zsh); do
    echo "=== pid $pid ==="
    tr '\0' '\n' < /proc/$pid/cmdline
done
```

查看最近一次的 in 快照：

```bash
ls -t /tmp/zsh-state-in-*.txt | head -1 | xargs cat
```

## 3. 错误现象

### 现象 A: `path: inconsistent type for assignment`

```
(eval):export:N: path: inconsistent type for assignment
```

来源：`path` / `fpath` 被 dump 成 scalar 形式 `export path='...'`。

### 现象 B: `bad pattern`

```
(eval):99: bad pattern: ...use:lib/history.zsh from:oh-my-zsh ...export _zplug_boolean_true=true
```

来源：`_zplug_tags` 等关联数组被 scalar dump，
值里 `\'` 转义错乱，把后续多行连成一坨被 zsh 当模式匹配。

## 4. 责任划分

| 侧 | 说明 |
|---|---|
| **CodeBuddy** | dump 函数缺陷：用 `${(k)parameters[(R)*export*]}` + `printf "export %s='%s'"` 写所有 export 变量，没有按变量类型分别处理 array-tied / associative array → **真 bug** |
| **zplug** | `typeset -gx path/fpath` 是合法但不必要的写法（`path` 本就跟 `PATH` 同步，无需再 export）；又把一批内部状态数组标记 export → **历史遗留写法** |
| **用户** | 无错 |

CodeBuddy 端真正应该改的地方：
**用 `typeset -p name` 输出**——zsh 会自动给出可重建的声明，
包括 `-a` / `-A` 类型标志和正确的转义。

## 5. 用户侧修复（已落地于 `~/.zshrc`）

在 `~/.zshrc` 末尾、`zplug load` 之后，加上：

```zsh
# 修复: zplug 把一批内部数组/关联数组也加了 export 标志,
# 会让 CodeBuddy 等 IDE 的 dump_zsh_state 误把它们当 scalar 写入会话快照,
# 下次 eval 时报错 (path: inconsistent type / bad pattern 等)。
# 这里统一去掉 export 标志,不影响 zplug 自身功能与 PATH/FPATH 同步。
typeset +x path fpath
typeset +x _zplug_status _zplug_load_log _zplug_boolean_false zplugs \
           _zplug_options _zplug_yaml _zplug_build_log _zplug_cache \
           em _zplug_log _zplug_tags _zplug_lock _zplug_commands \
           _zplug_boolean_true 2>/dev/null
```

### 原理

`typeset +x` 取消 export 标志（`+` 在 `typeset` 里表示关闭属性），
变量从 `parameters[(R)*export*]` 的匹配集合中移除，
dump 函数自然枚举不到它们 → 写出的快照就干净了。

数组本身的内容（去重、tied 关联等）保持不变，zplug 功能完全保留。

### 受影响变量清单（实测）

zplug 一共把 14 个内部变量标记为 export（其中 12 个 associative、2 个 array）：

```
_zplug_status, _zplug_load_log, _zplug_boolean_false, zplugs,
_zplug_options, _zplug_yaml, _zplug_build_log, _zplug_cache,
em, _zplug_log, _zplug_tags, _zplug_lock, _zplug_commands,
_zplug_boolean_true
```

复现命令：

```bash
zsh -i -c 'for v in ${(k)parameters[(R)*export*]}; do
  t=${parameters[$v]};
  case $t in *association*|*array*) echo "$v -> $t" ;; esac;
done'
```

## 6. 验证

修复后连续 4 次 IDE shell 调用，stderr 全程干净；快照内容验证：

```bash
F=$(ls -t /tmp/zsh-state-in-*.txt | head -1)
grep -cE '^export _zplug_' "$F"                          # → 0
grep -nE '^export (path|fpath|cdpath|manpath)=' "$F"     # → 无输出
```

注意：**修复落地后的"第一次"调用仍可能报错**——因为那次 IDE shell 进入时
`eval` 用的还是修复**之前** dump 出来的污染快照。
从第二次起就完全干净了，且后续不会再复发。

## 7. 备选方案对比

| 方案 | 做法 | 适用场景 |
|---|---|---|
| **A. 在 IDE shell 中完全跳过 zplug** | `.zshrc` 顶部判 `$CODEBUDDY_COPILOT_INTERNET_ENVIRONMENT` 或 `$VSCODE_IPC_HOOK_CLI` 非空时 `return` | 不依赖 zplug 补全等功能、追求 IDE shell 启动速度 |
| **B. 仅修 export 标志（本文采用）** | `zplug load` 之后追加 `typeset +x ...` | 想在 IDE shell 里也保留 zplug 补全/插件功能 |
| **C. 改用 bash** | IDE 配置里把默认 shell 换成 bash | agent 工具未必受 IDE 终端 profile 控制，收益不稳定，不推荐 |

## 8. 延伸知识：`typeset` 备忘

| 选项 | 含义 |
|---|---|
| `-g` | global |
| `-x` | export（等价于 `export`）；`+x` 取消 |
| `-r` | readonly |
| `-i` / `-F` / `-E` | 整数 / 浮点 |
| `-a` / `-A` | 数组 / 关联数组 |
| `-U` | 数组自动去重 |
| `-T NAME name sep` | tied，把 scalar 与 array 绑定（`PATH`/`path` 即此机制） |
| `-l` / `-u` | 赋值时自动转小写 / 大写 |
| `-p` | 以可重新执行的形式打印声明（dump 应该用这个） |

zplug 的 `typeset -gx -U path`：global + export + unique。
其中 `-x` 是多余且有副作用的——本 bug 的源头。

## 9. 关键观察

1. **每次 IDE 命令都是新 zsh 进程**，PID 每次都不一样；
   通过 `/tmp/zsh-state-{in,out}-*.txt` 在进程间传递状态。
2. **dump 函数的根本缺陷**：用 scalar `printf` 输出所有 export 变量；
   正确做法应是 `typeset -p` 让 zsh 自己生成可重建声明。
3. **黑名单已包含** `VSCODE_*` / `ELECTRON_*` / `NODE_OPTIONS` /
   `FZF_*` / `ATUIN_*` 等——说明 IDE 团队意识到了部分污染问题，
   只是 array-tied 和 associative 这两类还没补上。
4. **用户侧 `typeset +x` 是最干净的规避**：
   不改 zplug、不改 IDE，只让自己的变量从 dump 枚举集合中消失。
