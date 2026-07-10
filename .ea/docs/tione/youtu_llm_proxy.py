"""
Youtu LLM Proxy  (方案 C sidecar)
──────────────────────────────────
将 Anthropic Messages API 请求转发到腾讯内部 tRPC 服务，
对外暴露标准 Anthropic /v1/messages 接口。

配置（.ea/.env 或环境变量）：
  YOUTU_LLM_BASE         内部服务地址，默认 http://112.65.194.90:8001
  YOUTU_LLM_USERNAME     sec_info.username
  YOUTU_LLM_USERID       sec_info.userid
  YOUTU_LLM_TOKEN        sec_info.token
  YOUTU_LLM_PROXY_PORT   监听端口，默认 8088

使用：
  # 从 sub2api 根目录启动
  python .ea/youtu_llm_proxy.py

  # 或指定 .env 文件
  python .ea/youtu_llm_proxy.py --env .ea/.env

测试（通过 Anthropic SDK）：
  import anthropic
  client = anthropic.Anthropic(api_key="dummy", base_url="http://localhost:8088")
  msg = client.messages.create(model="claude-opus-4-6", max_tokens=256,
                               messages=[{"role": "user", "content": "hi"}])
"""

import argparse
import json
import logging
import os
from pathlib import Path
from typing import Any, Dict, List, Optional

import httpx
import uvicorn
from fastapi import FastAPI, Request, HTTPException
from fastapi.responses import JSONResponse, StreamingResponse

# ── Logging ───────────────────────────────────────────────────────────────────

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s [youtu-proxy] %(message)s",
)
log = logging.getLogger(__name__)


# ── Config (from env, loaded before app init) ─────────────────────────────────

def load_env_file(path: str) -> None:
    p = Path(path)
    if not p.exists():
        return
    for line in p.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        os.environ.setdefault(k.strip(), v.strip())


def get_config() -> dict:
    base = os.environ.get("YOUTU_LLM_BASE", "http://112.65.194.90:8001").rstrip("/")
    return {
        "stream_url": f"{base}/trpc.youtu.llm_interface_service.Greeter/DescribeLlmResultStreamSSE",
        "sync_url":   f"{base}/trpc.youtu.llm_interface_service.Greeter/DescribeLlmResult",
        "sec_info": {
            "username": os.environ.get("YOUTU_LLM_USERNAME", ""),
            "userid":   os.environ.get("YOUTU_LLM_USERID", ""),
            "token":    os.environ.get("YOUTU_LLM_TOKEN", ""),
        },
        "port": int(os.environ.get("YOUTU_LLM_PROXY_PORT", "8088")),
    }


# ── App ───────────────────────────────────────────────────────────────────────

app = FastAPI(title="Youtu LLM Proxy")

FORWARD_HEADERS = {
    "Content-Type": "application/json",
    "User-Agent":   "ifbook-http-client",
}


def build_payload(cfg: Dict[str, Any], body: Dict[str, Any]) -> Dict[str, Any]:
    """Wrap Anthropic-format body into the internal tRPC payload."""
    model_name = body.get("model", "claude-opus-4-6")
    # Always use streaming internally; non-streaming is handled by collecting SSE
    upstream_body = {**body, "stream": True}
    return {
        "sec_info":   cfg["sec_info"],
        "model_name": model_name,
        "params":     json.dumps(upstream_body),
    }


@app.get("/health")
async def health():
    return {"status": "ok", "service": "youtu-llm-proxy"}


@app.post("/v1/messages")
async def messages(request: Request):
    body = await request.json()
    is_stream = body.get("stream", False)
    cfg = get_config()

    if not cfg["sec_info"]["token"]:
        raise HTTPException(status_code=500, detail="YOUTU_LLM_TOKEN not configured")

    payload = build_payload(cfg, body)
    log.info("model=%s stream=%s", body.get("model"), is_stream)

    if is_stream:
        return await _stream_response(cfg, payload)
    else:
        return await _collect_stream_as_sync(cfg, payload)


# ── Streaming ─────────────────────────────────────────────────────────────────

async def _stream_response(cfg: Dict[str, Any], payload: Dict[str, Any]) -> StreamingResponse:
    async def event_generator():
        try:
            async with httpx.AsyncClient(timeout=300, trust_env=False) as client:
                async with client.stream(
                    "POST", cfg["stream_url"],
                    json=payload,
                    headers=FORWARD_HEADERS,
                ) as resp:
                    log.info("upstream status=%s", resp.status_code)
                    if resp.status_code != 200:
                        body_bytes = await resp.aread()
                        log.error("upstream error: %s", body_bytes.decode()[:300])
                        yield f"data: {json.dumps({'type': 'error', 'error': {'type': 'upstream_error', 'message': body_bytes.decode()[:300]}})}\n\n".encode()
                        return
                    async for chunk in resp.aiter_bytes():
                        yield chunk
        except httpx.RequestError as exc:
            log.error("upstream stream failed: %s", exc)
            yield f"data: {json.dumps({'type': 'error', 'error': {'type': 'connection_error', 'message': str(exc)}})}\n\n".encode()

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={
            "Cache-Control":    "no-cache",
            "X-Accel-Buffering": "no",
            "Connection":        "keep-alive",
        },
    )


# ── Non-streaming (collect SSE → assemble sync response) ─────────────────────

def _assemble_message(lines: List[str]) -> Dict[str, Any]:
    """Parse Anthropic SSE lines into a complete sync message object."""
    message: Dict[str, Any] = {}
    content_blocks: List[Dict[str, Any]] = []
    current_block: Optional[Dict[str, Any]] = None

    for line in lines:
        if not line.startswith("data: "):
            continue
        try:
            data = json.loads(line[6:])
        except json.JSONDecodeError:
            continue

        t = data.get("type")
        if t == "message_start":
            message = data.get("message", {})
        elif t == "content_block_start":
            current_block = dict(data.get("content_block", {}))
            btype = current_block.get("type")
            if btype == "text":
                current_block.setdefault("text", "")
            elif btype == "thinking":
                current_block.setdefault("thinking", "")
                current_block.setdefault("signature", "")
            elif btype == "tool_use":
                current_block["_parts"] = []
        elif t == "content_block_delta" and current_block:
            delta = data.get("delta", {})
            dt = delta.get("type")
            if dt == "text_delta":
                current_block["text"] = current_block.get("text", "") + delta.get("text", "")
            elif dt == "thinking_delta":
                current_block["thinking"] = current_block.get("thinking", "") + delta.get("thinking", "")
            elif dt == "signature_delta":
                current_block["signature"] = current_block.get("signature", "") + delta.get("signature", "")
            elif dt == "input_json_delta":
                current_block.setdefault("_parts", []).append(delta.get("partial_json", ""))
        elif t == "content_block_stop" and current_block is not None:
            if current_block.get("type") == "tool_use":
                raw = "".join(current_block.pop("_parts", []))
                try:
                    current_block["input"] = json.loads(raw)
                except json.JSONDecodeError:
                    current_block["input"] = {}
            content_blocks.append(current_block)
            current_block = None
        elif t == "message_delta":
            delta = data.get("delta", {})
            message.update(delta)
            usage = data.get("usage", {})
            if usage:
                message.setdefault("usage", {}).update(usage)

    message["content"] = content_blocks
    return message


async def _collect_stream_as_sync(cfg: Dict[str, Any], payload: Dict[str, Any]) -> JSONResponse:
    """Call the stream endpoint and assemble into a sync Anthropic message response."""
    try:
        lines: list[str] = []
        async with httpx.AsyncClient(timeout=300, trust_env=False) as client:
            async with client.stream(
                "POST", cfg["stream_url"],
                json=payload,
                headers=FORWARD_HEADERS,
            ) as resp:
                log.info("upstream status=%s", resp.status_code)
                if resp.status_code != 200:
                    body = await resp.aread()
                    log.error("upstream error: %s", body.decode()[:500])
                    return JSONResponse(
                        content={"error": {"type": "upstream_error", "message": body.decode()[:300]}},
                        status_code=502,
                    )
                async for chunk in resp.aiter_lines():
                    lines.append(chunk)
        message = _assemble_message(lines)
        log.info(
            "assembled message stop_reason=%s blocks=%d",
            message.get("stop_reason"),
            len(message.get("content", [])),
        )
        return JSONResponse(content=message)
    except httpx.RequestError as exc:
        log.error("upstream request failed: %s", exc)
        return JSONResponse(
            content={"error": {"type": "connection_error", "message": str(exc)}},
            status_code=502,
        )


# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Youtu LLM → Anthropic API proxy")
    parser.add_argument("--env",  default=".ea/.env", help=".env 文件路径")
    parser.add_argument("--port", type=int, default=0,   help="监听端口（覆盖 .env 中的值）")
    parser.add_argument("--host", default="127.0.0.1",   help="监听地址（生产环境用 0.0.0.0）")
    args = parser.parse_args()

    load_env_file(args.env)
    cfg = get_config()
    port = args.port if args.port else cfg["port"]

    log.info("Youtu LLM Proxy starting on %s:%d", args.host, port)
    log.info("Upstream: %s", os.environ.get("YOUTU_LLM_BASE", "http://112.65.194.90:8001"))
    log.info("sec_info.userid: %s", cfg["sec_info"]["userid"])

    uvicorn.run(app, host=args.host, port=port, log_level="info")
