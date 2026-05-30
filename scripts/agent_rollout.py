#!/usr/bin/env python3
"""End-to-end agent rollout on a Tencent AGS sandbox.

流程:
1. 校验本机所需环境变量（AGS 数据面 + Anthropic 凭据）
2. 在 AGS 创建一个真实 sandbox（共享 tool/runtime image）
3. 在 sandbox 内通过 npm 安装 ``@anthropic-ai/claude-code``
4. 让 ``claude -p`` 在沙箱工作目录里写出 ``fizzbuzz.py`` 并执行
5. 校验脚本 stdout 命中 FizzBuzz/Fizz/Buzz 关键词
6. **保留 sandbox 不 kill**，输出 sandbox_id 与后续手动连接提示

设计:
- 不污染 skill 自带 ``scripts/`` 目录，仅复用 ``_common`` 中的 ``.env`` 加载
  与 e2b API key 校验补丁；脚本本身放在项目 ``scripts/`` 下。
- Anthropic 凭据通过 ``commands.run(envs=...)`` 注入，避免落盘。
- 异常分级：sandbox 创建失败时不会有可保留的实例；其它阶段失败时
  仍保留 sandbox 以便排查。
"""

from __future__ import annotations

import asyncio
import base64
import json
import os
import shlex
import sys
import time
from pathlib import Path

# 复用 skill 内的 _common（.env 加载 + e2b api key 补丁）
SKILL_SCRIPTS = Path("/root/sub2api/.agents/skills/ags-sandbox-helper/scripts")
sys.path.insert(0, str(SKILL_SCRIPTS))

from _common import masked, require_env, run_async_compat  # noqa: E402

from e2b import AsyncSandbox  # noqa: E402

# ---------- 默认值（可被环境变量覆盖） ----------

DEFAULTS = {
    "AGS_TOOL_NAME": "sdt-535ctmbe",
    "AGS_RUNTIME_IMAGE": "useast.tencentcloudcr.com/eason/ags-test:base-envd",
    "AGS_IMAGE_REGISTRY_TYPE": "enterprise",
    "TENCENTCLOUD_REGION": "na-ashburn",
    "AGS_ROLLOUT_CPU": "2",
    "AGS_ROLLOUT_MEMORY": "4Gi",
    "AGS_ROLLOUT_TIMEOUT": "1800",  # sandbox 整体存活 30min
    "AGS_ROLLOUT_WORKDIR": "/home/user/work",
    "AGS_ROLLOUT_AGENT_USER": "user",  # claude --dangerously-skip-permissions 不允许 root
    # 默认 model：默认 claude-opus-4-* 带 [1m] flag 中转网关常常不支持。
    # 这里默认走 sonnet-4-5（已验证大多数中转都接受）。可被 ANTHROPIC_MODEL 覆盖。
    "AGS_ROLLOUT_MODEL": "claude-sonnet-4-5",
}


def env_or_default(name: str) -> str:
    return os.environ.get(name, DEFAULTS.get(name, "")).strip()


# ---------- Sandbox 配置 ----------

def build_custom_config() -> dict:
    return {
        "image": env_or_default("AGS_RUNTIME_IMAGE"),
        "imageRegistryType": env_or_default("AGS_IMAGE_REGISTRY_TYPE"),
        "resources": {
            "cpu": env_or_default("AGS_ROLLOUT_CPU"),
            "memory": env_or_default("AGS_ROLLOUT_MEMORY"),
        },
    }


# ---------- Sandbox 内步骤 ----------

async def run_in_sandbox(
    sandbox: AsyncSandbox,
    cmd: str,
    *,
    label: str,
    envs: dict | None = None,
    timeout: int = 600,
    cwd: str | None = None,
    user: str = "root",
    expect_zero: bool = True,
) -> tuple[int, str, str]:
    """统一的 sandbox 命令执行 + 输出打印。"""
    print(f"\n[STEP] {label}")
    print(f"  $ {cmd}")
    if cwd:
        print(f"  cwd={cwd}")
    result = await sandbox.commands.run(
        cmd,
        timeout=timeout,
        user=user,
        cwd=cwd,
        envs=envs or {},
    )
    stdout = (result.stdout or "").rstrip()
    stderr = (result.stderr or "").rstrip()
    if stdout:
        print("  --- stdout ---")
        for line in stdout.splitlines():
            print(f"  {line}")
    if stderr:
        print("  --- stderr ---")
        for line in stderr.splitlines():
            print(f"  {line}")
    print(f"  exit_code={result.exit_code}")
    if expect_zero and result.exit_code != 0:
        raise RuntimeError(f"{label} 失败: exit_code={result.exit_code}")
    return result.exit_code, stdout, stderr


async def install_claude_cli(sandbox: AsyncSandbox) -> str:
    """在 sandbox 内安装 Claude Code CLI，返回可执行路径。

    优先策略:
      1. 已经存在 ``claude`` -> 直接返回
      2. 有 npm -> 全局安装 ``@anthropic-ai/claude-code``
      3. 否则报错（base-envd 通常自带 node/npm；缺失说明镜像不对）
    """
    code, stdout, _ = await run_in_sandbox(
        sandbox,
        "command -v claude || true",
        label="探测 claude CLI",
        timeout=15,
    )
    if stdout.strip():
        return stdout.strip()

    code, _, _ = await run_in_sandbox(
        sandbox,
        "command -v npm && node --version && npm --version",
        label="探测 node/npm",
        timeout=15,
    )

    await run_in_sandbox(
        sandbox,
        "npm install -g @anthropic-ai/claude-code 2>&1 | tail -20",
        label="npm 全局安装 claude CLI",
        timeout=300,
    )

    _, stdout, _ = await run_in_sandbox(
        sandbox,
        "command -v claude && claude --version",
        label="确认 claude 可用",
        timeout=15,
    )
    return stdout.splitlines()[0].strip()


# ---------- Rollout 主流程 ----------

PROMPT = """\
You are inside a sandbox. Your task:

1. In the current directory, create a file named `fizzbuzz.py` that prints
   the classic FizzBuzz output for numbers 1 through 15 (one per line):
     - multiples of 3 -> "Fizz"
     - multiples of 5 -> "Buzz"
     - multiples of both 3 and 5 -> "FizzBuzz"
     - otherwise the number itself.
2. Run the script with `python3 fizzbuzz.py` and verify the output looks right.
3. Reply with the literal string `DONE` once everything works.

Do not ask for confirmation. Use your tools directly.
"""


async def run_rollout() -> int:
    # 数据面 + agent 凭据校验
    require_env("E2B_API_KEY")
    require_env("E2B_DOMAIN")
    require_env("AGS_TOOL_NAME") if os.environ.get("AGS_TOOL_NAME") else None
    tool_name = env_or_default("AGS_TOOL_NAME")
    if not tool_name:
        raise SystemExit("缺少 AGS_TOOL_NAME（或脚本默认值）")
    runtime_image = env_or_default("AGS_RUNTIME_IMAGE")
    if not runtime_image:
        raise SystemExit("缺少 AGS_RUNTIME_IMAGE")

    # Anthropic 凭据。两套都可用：
    #   - 官方:  ANTHROPIC_API_KEY
    #   - 中转/代理: ANTHROPIC_AUTH_TOKEN (+ ANTHROPIC_BASE_URL)
    # 当两套同时存在时，默认优先中转（更常见的工作场景），
    # 可用 AGS_ROLLOUT_PREFER=official 强制走官方。
    anthropic_api_key = os.environ.get("ANTHROPIC_API_KEY", "").strip()
    anthropic_auth_token = os.environ.get("ANTHROPIC_AUTH_TOKEN", "").strip()
    anthropic_base_url = os.environ.get("ANTHROPIC_BASE_URL", "").strip()
    prefer = os.environ.get("AGS_ROLLOUT_PREFER", "proxy").strip().lower()

    use_proxy = False
    if prefer == "official" and anthropic_api_key:
        use_proxy = False
    elif anthropic_auth_token:
        use_proxy = True
    elif anthropic_api_key:
        use_proxy = False
    else:
        raise SystemExit(
            "缺少 Anthropic 凭据：需要 ANTHROPIC_API_KEY 或 "
            "ANTHROPIC_AUTH_TOKEN(+ANTHROPIC_BASE_URL)"
        )

    workdir = env_or_default("AGS_ROLLOUT_WORKDIR")
    timeout = int(env_or_default("AGS_ROLLOUT_TIMEOUT") or "1800")

    print("=" * 70)
    print("Agent Rollout (Claude Code @ Tencent AGS sandbox)")
    print("=" * 70)
    print(f"  tool      = {tool_name}")
    print(f"  image     = {runtime_image}")
    print(f"  region    = {env_or_default('TENCENTCLOUD_REGION')}")
    print(f"  workdir   = {workdir}")
    if use_proxy:
        print(f"  auth      = ANTHROPIC_AUTH_TOKEN ({masked(anthropic_auth_token)})")
        print(f"  base_url  = {anthropic_base_url or '(unset)'}")
    else:
        print(f"  auth      = ANTHROPIC_API_KEY ({masked(anthropic_api_key)})")
    print()

    sandbox: AsyncSandbox | None = None
    sandbox_kept = False
    started = time.time()
    try:
        # -------- 1. 创建 sandbox --------
        print(f"[STEP] 创建 sandbox …")
        sandbox = await AsyncSandbox.create(
            template=tool_name,
            timeout=timeout,
            metadata={
                "x-custom-config": json.dumps(build_custom_config()),
                "environment_name": "ags-rollout",
                "session_id": f"ags-rollout-{int(started)}",
            },
        )
        print(f"  sandbox_id={sandbox.sandbox_id}, elapsed={time.time() - started:.1f}s")

        # -------- 2. 准备工作目录（root 创建并 chown 给 agent user） --------
        agent_user = env_or_default("AGS_ROLLOUT_AGENT_USER")
        await run_in_sandbox(
            sandbox,
            (
                f"mkdir -p {shlex.quote(workdir)} && "
                f"chown -R {shlex.quote(agent_user)}:{shlex.quote(agent_user)} "
                f"{shlex.quote(workdir)} && "
                f"ls -la {shlex.quote(workdir)}"
            ),
            label=f"准备工作目录 {workdir} 并 chown 给 {agent_user}",
            timeout=15,
        )

        # -------- 3. 安装 Claude CLI --------
        claude_path = await install_claude_cli(sandbox)
        print(f"  claude_path={claude_path}")

        # -------- 4. 跑 agent prompt --------
        envs: dict[str, str] = {}
        if use_proxy:
            envs["ANTHROPIC_AUTH_TOKEN"] = anthropic_auth_token
            if anthropic_base_url:
                envs["ANTHROPIC_BASE_URL"] = anthropic_base_url
            # 防止 SDK 同时拿到官方 key 走错通道
            envs["ANTHROPIC_API_KEY"] = ""
        else:
            envs["ANTHROPIC_API_KEY"] = anthropic_api_key
        # 关闭 Claude Code 在沙箱内对外发的遥测/更新检查（更稳）
        envs.setdefault("DISABLE_TELEMETRY", "1")
        envs.setdefault("DISABLE_AUTOUPDATER", "1")
        envs.setdefault("DISABLE_BUG_COMMAND", "1")
        envs.setdefault("DISABLE_NON_ESSENTIAL_MODEL_CALLS", "1")
        # 显式指定 model：默认 opus 系列在中转网关常因 [1m] context flag 触发 502。
        # 优先使用环境变量 ANTHROPIC_MODEL，其次脚本默认。
        model = (
            os.environ.get("ANTHROPIC_MODEL", "").strip()
            or env_or_default("AGS_ROLLOUT_MODEL")
        )
        envs["ANTHROPIC_MODEL"] = model
        print(f"  model     = {model}")

        prompt_b64 = base64.b64encode(PROMPT.encode()).decode()
        # 通过 base64 管道注入 prompt 到 stdin，避免 shell 转义/长度问题
        agent_cmd = (
            f"printf '%s' {shlex.quote(prompt_b64)} | base64 -d | "
            f"claude --dangerously-skip-permissions --output-format text "
            f"--model {shlex.quote(model)} -p"
        )
        await run_in_sandbox(
            sandbox,
            agent_cmd,
            label=f"运行 Claude agent (fizzbuzz, user={agent_user})",
            envs=envs,
            timeout=600,
            cwd=workdir,
            user=agent_user,
        )

        # -------- 5. 验证 agent 真的写了文件并能跑出预期输出 --------
        await run_in_sandbox(
            sandbox,
            "ls -la && echo '----' && cat fizzbuzz.py",
            label="确认 fizzbuzz.py 已生成",
            timeout=15,
            cwd=workdir,
            user=agent_user,
        )
        _, stdout, _ = await run_in_sandbox(
            sandbox,
            "python3 fizzbuzz.py",
            label="独立运行 fizzbuzz.py 复核输出",
            timeout=30,
            cwd=workdir,
            user=agent_user,
        )
        # 校验关键 token
        required_tokens = ["1", "Fizz", "Buzz", "FizzBuzz", "14"]
        missing = [t for t in required_tokens if t not in stdout]
        if missing:
            raise RuntimeError(f"fizzbuzz 输出缺少关键 token: {missing}\n实际输出:\n{stdout}")
        print("\n[PASS] fizzbuzz 输出包含全部关键字段")

        # -------- 6. 保留 sandbox --------
        sandbox_kept = True
        print()
        print("=" * 70)
        print("ROLLOUT SUCCESS — sandbox 已保留，未 kill")
        print("=" * 70)
        print(f"  sandbox_id : {sandbox.sandbox_id}")
        print(f"  workdir    : {workdir}")
        print(f"  存活上限   : {timeout}s（从创建时刻起）")
        print()
        print("手动连接示例 (在本机 venv 里):")
        print()
        print("  python3 - <<'PY'")
        print("  import asyncio, os")
        print("  from e2b import AsyncSandbox")
        print(f"  sid = {sandbox.sandbox_id!r}")
        print("  async def main():")
        print("      sb = await AsyncSandbox.connect(sid)")
        print(f"      r = await sb.commands.run('ls -la {workdir}', user='root')")
        print("      print(r.stdout)")
        print("  asyncio.run(main())")
        print("  PY")
        print()
        print("用完后手动 kill:")
        print(f"  python3 -c \"import asyncio; from e2b import AsyncSandbox;"
              f" asyncio.run(AsyncSandbox.connect({sandbox.sandbox_id!r}).kill())\"")
        return 0

    finally:
        if sandbox is not None and not sandbox_kept:
            # 仅在失败时按用户偏好仍保留？需求里"跑完保留" -> 即使失败也保留
            # 这里也保留，方便排错；只在 stderr 提醒
            print(
                f"\n[NOTE] rollout 中途失败，按你的清理策略仍保留 sandbox: "
                f"{sandbox.sandbox_id}"
            )
            print(
                f"       排查完后手动 kill: python3 -c \\\n"
                f"         \"import asyncio; from e2b import AsyncSandbox;\\\n"
                f"          asyncio.run(AsyncSandbox.connect({sandbox.sandbox_id!r}).kill())\""
            )


def main() -> int:
    try:
        return run_async_compat(run_rollout())
    except KeyboardInterrupt:
        print("\n[ABORT] 用户中断")
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
