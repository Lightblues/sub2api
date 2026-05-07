"""
Sub2API Request Log Viewer - FastAPI Backend

Reads the SQLite database written by sub2api Go backend and serves a web UI.
No JSONL sync needed — sub2api writes directly to SQLite.
"""

import os
import json
import re
import subprocess
from contextlib import asynccontextmanager
from pathlib import Path
from datetime import datetime
from typing import Optional

import yaml
from fastapi import FastAPI, Query, HTTPException
from fastapi.responses import HTMLResponse, StreamingResponse, JSONResponse

try:
    from fastapi.responses import ORJSONResponse
    DefaultResponse = ORJSONResponse
except ImportError:
    DefaultResponse = JSONResponse

from sub2api_inspector.db import (
    open_db, get_stats, extract_session,
    query_logs, get_record_by_id, query_export, get_dates,
    set_archive_dir, resolve_db,
)

# --- Config ---
DB_PATH = os.environ.get(
    "INSPECTOR_DB",
    "/data/docker/lib/volumes/sub2api_sub2api_data/_data/request_logs/request_log.db",
)
ARCHIVE_DIR = os.environ.get("INSPECTOR_ARCHIVE_DIR", "")
CONFIG_PATH = os.environ.get("CONFIG_PATH", "")

_CONFIG_CANDIDATES = [
    "/root/sub2api/config.yaml",
    "/data/docker/lib/volumes/sub2api_sub2api_data/_data/config.yaml",
]

_db = None


def _get_db():
    global _db
    if _db is None:
        raise HTTPException(503, "Database not initialized yet")
    return _db


# --- Lifespan ---

@asynccontextmanager
async def lifespan(app: FastAPI):
    global _db
    print(f"Database path: {DB_PATH}")
    if not Path(DB_PATH).exists():
        print(f"[WARN] Database not found: {DB_PATH}")
        print(f"  sub2api must be running and writing to this path.")
        print(f"  Set INSPECTOR_DB env var to override.")
    _db = open_db(DB_PATH)
    try:
        _db.execute("SELECT COUNT(*) FROM records").fetchone()
        print("Database connected, records table found.")
    except Exception:
        print("[WARN] records table not found — sub2api may not have written any logs yet.")
    if ARCHIVE_DIR:
        set_archive_dir(ARCHIVE_DIR)
        print(f"Archive directory: {ARCHIVE_DIR}")
    yield
    if _db:
        _db.close()
        _db = None


app = FastAPI(title="Sub2API Log Viewer", default_response_class=DefaultResponse, lifespan=lifespan)


# --- PostgreSQL helpers (for API key lookup) ---

def _load_db_config() -> dict | None:
    global CONFIG_PATH
    paths = [CONFIG_PATH] if CONFIG_PATH else _CONFIG_CANDIDATES
    for p in paths:
        try:
            with open(p) as f:
                cfg = yaml.safe_load(f)
            if cfg and cfg.get("database"):
                CONFIG_PATH = p
                return cfg["database"]
        except Exception:
            continue
    return None


def _query_pg(sql: str, db_cfg: dict) -> str:
    env = {**os.environ, "PGPASSWORD": db_cfg["password"]}
    cmd = [
        "docker", "exec",
        "-e", "PGPASSWORD",
        "sub2api-postgres",
        "psql", "-U", db_cfg["user"], "-d", db_cfg["dbname"],
        "-t", "-A", "-F", "\t",
        "-c", sql,
    ]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=10, env=env)
    if result.returncode != 0:
        raise RuntimeError(result.stderr.strip())
    return result.stdout.strip()


# --- Helpers ---

def _validate_date(date: str):
    try:
        datetime.strptime(date, "%Y-%m-%d")
    except ValueError:
        raise HTTPException(400, "Invalid date format, expected YYYY-MM-DD")


def _sanitize_filename(s: str) -> str:
    return re.sub(r"[^\w\-.]", "_", s)


# --- API Endpoints ---

@app.get("/api/dates")
def list_dates():
    """List available log dates from SQLite."""
    db = _get_db()
    try:
        dates = get_dates(db)
        return {"dates": dates}
    except Exception:
        return {"dates": []}


@app.get("/api/logs/{date}/stats")
def log_stats(date: str):
    _validate_date(date)
    db = resolve_db(_get_db(), date)
    stats = get_stats(db, date)
    if stats:
        return stats
    raise HTTPException(404, f"No data for {date}")


@app.get("/api/logs/{date}")
def get_logs(
    date: str,
    session_id: Optional[str] = None,
    api_key_id: Optional[int] = None,
    model: Optional[str] = None,
    q: Optional[str] = None,
    offset: int = Query(0, ge=0),
    limit: int = Query(100, ge=1, le=1000),
):
    _validate_date(date)
    db = resolve_db(_get_db(), date)
    results, total_matched = query_logs(
        db, date, session_id, api_key_id, model, q, offset, limit
    )
    return {
        "date": date,
        "total_matched": total_matched,
        "offset": offset,
        "limit": limit,
        "data": results,
    }


@app.get("/api/logs/{date}/record/{record_id}")
def get_record(date: str, record_id: int):
    _validate_date(date)
    db = resolve_db(_get_db(), date)
    rec = get_record_by_id(db, record_id)
    if not rec:
        raise HTTPException(404, "Record not found")

    body = rec.get("request_body") or {}
    resp_data = (rec.get("response_complete") or {}).get("response") or {}
    usage = resp_data.get("usage") or {}
    h = rec.get("request_headers") or {}
    session_type, session_id = extract_session(rec)
    session = {"type": session_type, "id": session_id} if session_id else None

    ts_val = rec.get("ts")
    summary = {
        "id": record_id,
        "ts": ts_val,
        "time": datetime.fromtimestamp(ts_val).strftime("%H:%M:%S") if ts_val else None,
        "api_key_id": rec.get("api_key_id"),
        "model": body.get("model") or resp_data.get("model"),
        "session": session,
        "user_agent": h.get("user-agent", "")[:80],
        "status": resp_data.get("status"),
        "input_tokens": usage.get("input_tokens"),
        "output_tokens": usage.get("output_tokens"),
        "cached_tokens": (usage.get("input_tokens_details") or {}).get("cached_tokens"),
        "reasoning_tokens": (usage.get("output_tokens_details") or {}).get("reasoning_tokens"),
        "total_tokens": usage.get("total_tokens"),
    }
    return {"record": rec, "summary": summary}


@app.get("/api/logs/{date}/export")
def export_logs(
    date: str,
    session_id: Optional[str] = None,
    api_key_id: Optional[int] = None,
    model: Optional[str] = None,
    q: Optional[str] = None,
):
    _validate_date(date)
    db = resolve_db(_get_db(), date)
    fname = f"{date}"
    if session_id:
        fname += f"_session-{_sanitize_filename(session_id[:12])}"
    if api_key_id is not None:
        fname += f"_key-{api_key_id}"
    if model:
        fname += f"_{_sanitize_filename(model)}"
    fname += ".jsonl"

    def generate():
        for raw_json in query_export(db, date, session_id, api_key_id, model, q):
            yield raw_json.encode("utf-8") + b"\n"

    return StreamingResponse(
        generate(),
        media_type="application/x-ndjson",
        headers={"Content-Disposition": f'attachment; filename="{fname}"'},
    )


# --- API Key lookup ---

@app.get("/api/keys")
def list_keys():
    db_cfg = _load_db_config()
    if not db_cfg:
        raise HTTPException(503, "Database config not available")
    try:
        raw = _query_pg(
            "SELECT id, name, LEFT(key, 12) || '...' FROM api_keys ORDER BY id;",
            db_cfg,
        )
        keys = []
        for line in raw.split("\n"):
            if not line.strip():
                continue
            parts = line.split("\t")
            if len(parts) >= 3:
                keys.append({"id": int(parts[0]), "name": parts[1], "key_prefix": parts[2]})
        return {"keys": keys}
    except Exception as e:
        raise HTTPException(500, f"DB query failed: {e}")


@app.get("/api/keys/lookup")
def lookup_key(sk: str = Query(..., description="Full or partial API key (sk-...)")):
    db_cfg = _load_db_config()
    if not db_cfg:
        raise HTTPException(503, "Database config not available")
    safe_sk = sk.replace("'", "''").replace("%", "").replace("_", "\\_")
    try:
        raw = _query_pg(
            f"SELECT id, name, LEFT(key, 20) || '...' FROM api_keys WHERE key LIKE '{safe_sk}%' LIMIT 10;",
            db_cfg,
        )
        keys = []
        for line in raw.split("\n"):
            if not line.strip():
                continue
            parts = line.split("\t")
            if len(parts) >= 3:
                keys.append({"id": int(parts[0]), "name": parts[1], "key_prefix": parts[2]})
        return {"query": sk, "matches": keys}
    except Exception as e:
        raise HTTPException(500, f"DB query failed: {e}")


# --- Serve frontend ---

@app.get("/", response_class=HTMLResponse)
def index():
    html_path = Path(__file__).parent / "static" / "index.html"
    if not html_path.exists():
        raise HTTPException(404, "Frontend not found")
    return HTMLResponse(html_path.read_text(encoding="utf-8"))


def main():
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8019)


if __name__ == "__main__":
    main()
