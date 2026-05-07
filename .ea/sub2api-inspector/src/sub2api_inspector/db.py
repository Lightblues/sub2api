"""
SQLite query layer for sub2api-inspector (read-only).

The sub2api Go backend writes directly to the SQLite database.
This module only provides read queries and stats computation.

Supports an optional archive directory containing per-day SQLite files
(e.g. ``2026-03-13.db``) for data that has been rotated out of the main DB.
"""

import json
import logging
import sqlite3
import time
from datetime import datetime
from pathlib import Path
from typing import Optional

log = logging.getLogger(__name__)

try:
    import orjson
    def fast_loads(s): return orjson.loads(s)
    def fast_dumps(obj): return orjson.dumps(obj).decode()
except ImportError:
    def fast_loads(s): return json.loads(s)
    def fast_dumps(obj): return json.dumps(obj, ensure_ascii=False)


def open_db(db_path: str | Path) -> sqlite3.Connection:
    """Open the SQLite database read-only (written by sub2api Go backend)."""
    db = sqlite3.connect(str(db_path), check_same_thread=False, timeout=30)
    db.row_factory = sqlite3.Row
    db.execute("PRAGMA journal_mode=WAL")
    db.execute("PRAGMA busy_timeout=30000")
    return db


# ---------------------------------------------------------------------------
# Archive support
# ---------------------------------------------------------------------------

_archive_dir: Path | None = None
_archive_conns: dict[str, sqlite3.Connection] = {}


def set_archive_dir(path: str | Path | None) -> None:
    global _archive_dir
    if path:
        _archive_dir = Path(path)
        log.info("Archive directory: %s", _archive_dir)
    else:
        _archive_dir = None


def _open_archive_db(date: str) -> sqlite3.Connection | None:
    """Open a per-day archive SQLite file, with connection caching."""
    if not _archive_dir:
        return None
    if date in _archive_conns:
        return _archive_conns[date]
    db_path = _archive_dir / f"{date}.db"
    if not db_path.exists():
        return None
    try:
        db = sqlite3.connect(
            f"file:{db_path}?mode=ro", uri=True,
            check_same_thread=False, timeout=30,
        )
        db.row_factory = sqlite3.Row
        db.execute("PRAGMA busy_timeout=30000")
        _archive_conns[date] = db
        log.info("Opened archive DB: %s", db_path)
        return db
    except Exception:
        log.warning("Failed to open archive DB: %s", db_path, exc_info=True)
        return None


def resolve_db(main_db: sqlite3.Connection, date: str) -> sqlite3.Connection:
    """Return the DB that holds data for *date*: main_db if it has rows, else archive."""
    row = main_db.execute(
        "SELECT COUNT(*) as cnt FROM records WHERE date = ?", (date,)
    ).fetchone()
    if row and row["cnt"] > 0:
        return main_db
    archive = _open_archive_db(date)
    return archive if archive else main_db


def _list_archive_dates() -> list[dict]:
    """List dates available in the archive directory."""
    if not _archive_dir or not _archive_dir.is_dir():
        return []
    dates = []
    for p in sorted(_archive_dir.glob("*.db"), reverse=True):
        date_str = p.stem
        try:
            datetime.strptime(date_str, "%Y-%m-%d")
        except ValueError:
            continue
        adb = _open_archive_db(date_str)
        if adb:
            try:
                row = adb.execute("SELECT COUNT(*) as cnt FROM records").fetchone()
                dates.append({"date": date_str, "records": row["cnt"], "archived": True})
            except Exception:
                pass
    return dates


# ---------------------------------------------------------------------------
# Session extraction (for record detail endpoint)
# ---------------------------------------------------------------------------

def extract_session(rec: dict) -> tuple[Optional[str], Optional[str]]:
    """Extract (session_type, session_id) from a log record."""
    h = rec.get("request_headers") or {}
    if h.get("session_id"):
        return ("codex", h["session_id"])
    cc_sid = h.get("x-claude-code-session-id")
    if cc_sid:
        return ("claude-code", cc_sid)
    try:
        uid = (rec.get("request_body") or {}).get("metadata", {}).get("user_id", "")
        if uid and "{" in uid:
            parsed = fast_loads(uid)
            if parsed.get("session_id"):
                return ("claude-code", parsed["session_id"])
    except (json.JSONDecodeError, TypeError, AttributeError):
        pass
    return (None, None)


# ---------------------------------------------------------------------------
# Stats (computed on demand, cached in memory)
# ---------------------------------------------------------------------------

# In-memory stats cache: {date: (stats_dict, record_count_at_compute_time)}
_stats_cache: dict[str, tuple[dict, int]] = {}


def get_stats(db: sqlite3.Connection, date: str) -> dict | None:
    """Get aggregated stats for a date. Cached in memory, invalidated when row count changes."""
    # Check current row count
    row = db.execute("SELECT COUNT(*) as cnt FROM records WHERE date = ?", (date,)).fetchone()
    current_count = row["cnt"] if row else 0
    if current_count == 0:
        return None

    # Check cache
    if date in _stats_cache:
        cached_stats, cached_count = _stats_cache[date]
        if cached_count == current_count:
            return cached_stats

    # Compute fresh
    rows = db.execute(
        """SELECT model, api_key_id, session_type, session_id,
                  input_tokens, output_tokens, cached_tokens,
                  reasoning_tokens, total_tokens
           FROM records WHERE date = ? ORDER BY ts""",
        (date,),
    ).fetchall()

    total = len(rows)
    by_model: dict[str, int] = {}
    by_key: dict[int, int] = {}
    sessions: dict[str, dict] = {}
    total_input = total_output = total_cached = 0

    for r in rows:
        m = r["model"] or "unknown"
        by_model[m] = by_model.get(m, 0) + 1

        k = r["api_key_id"]
        if k is not None:
            by_key[k] = by_key.get(k, 0) + 1

        if r["session_id"]:
            key = f"{r['session_type']}:{r['session_id'][:12]}"
            if key not in sessions:
                sessions[key] = {
                    "type": r["session_type"],
                    "id": r["session_id"],
                    "count": 0,
                }
            sessions[key]["count"] += 1

        total_input += r["input_tokens"] or 0
        total_output += r["output_tokens"] or 0
        total_cached += r["cached_tokens"] or 0

    stats = {
        "date": date,
        "total_requests": total,
        "by_model": dict(sorted(by_model.items(), key=lambda x: -x[1])),
        "by_api_key_id": {str(k): v for k, v in sorted(by_key.items(), key=lambda x: -x[1])},
        "sessions": sorted(sessions.values(), key=lambda x: -x["count"]),
        "tokens": {
            "total_input": total_input,
            "total_output": total_output,
            "total_cached": total_cached,
            "total": total_input + total_output,
        },
    }

    _stats_cache[date] = (stats, current_count)
    return stats


# ---------------------------------------------------------------------------
# Query helpers
# ---------------------------------------------------------------------------

def _like_escape(s: str) -> str:
    """Escape LIKE wildcards in user input."""
    return s.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


def build_where(
    date: str,
    session_id: str | None = None,
    api_key_id: int | None = None,
    model: str | None = None,
    q: str | None = None,
) -> tuple[str, list]:
    """Build WHERE clause and params for filtered queries."""
    clauses = ["date = ?"]
    params: list = [date]

    if session_id:
        clauses.append("session_id LIKE ? ESCAPE '\\'")
        params.append(f"%{_like_escape(session_id)}%")
    if api_key_id is not None:
        clauses.append("api_key_id = ?")
        params.append(api_key_id)
    if model:
        clauses.append("model = ?")
        params.append(model)
    if q:
        clauses.append("raw_json LIKE ? ESCAPE '\\'")
        params.append(f"%{_like_escape(q)}%")

    return " AND ".join(clauses), params


def query_logs(
    db: sqlite3.Connection,
    date: str,
    session_id: str | None = None,
    api_key_id: int | None = None,
    model: str | None = None,
    q: str | None = None,
    offset: int = 0,
    limit: int = 100,
) -> tuple[list[dict], int]:
    """Query log records with filters. Returns (records, total_matched)."""
    where, params = build_where(date, session_id, api_key_id, model, q)

    count_row = db.execute(
        f"SELECT COUNT(*) as cnt FROM records WHERE {where}", params
    ).fetchone()
    total = count_row["cnt"]

    rows = db.execute(
        f"""SELECT id, ts, api_key_id, model, session_type, session_id,
                   status, input_tokens, output_tokens, cached_tokens,
                   reasoning_tokens, total_tokens
            FROM records WHERE {where}
            ORDER BY ts
            LIMIT ? OFFSET ?""",
        params + [limit, offset],
    ).fetchall()

    results = []
    for r in rows:
        ts_val = r["ts"]
        results.append({
            "id": r["id"],
            "ts": ts_val,
            "time": datetime.fromtimestamp(ts_val).strftime("%H:%M:%S") if ts_val else None,
            "api_key_id": r["api_key_id"],
            "model": r["model"],
            "session": {"type": r["session_type"], "id": r["session_id"]} if r["session_id"] else None,
            "status": r["status"],
            "input_tokens": r["input_tokens"],
            "output_tokens": r["output_tokens"],
            "cached_tokens": r["cached_tokens"],
            "reasoning_tokens": r["reasoning_tokens"],
            "total_tokens": r["total_tokens"],
        })

    return results, total


def get_record_by_id(db: sqlite3.Connection, record_id: int) -> dict | None:
    """Get a full record by its database ID."""
    row = db.execute(
        "SELECT raw_json FROM records WHERE id = ?", (record_id,)
    ).fetchone()
    if not row:
        return None
    return fast_loads(row["raw_json"])


def query_export(
    db: sqlite3.Connection,
    date: str,
    session_id: str | None = None,
    api_key_id: int | None = None,
    model: str | None = None,
    q: str | None = None,
):
    """Generator yielding raw_json lines for export."""
    where, params = build_where(date, session_id, api_key_id, model, q)
    cursor = db.execute(
        f"SELECT raw_json FROM records WHERE {where} ORDER BY ts", params
    )
    for row in cursor:
        yield row["raw_json"]


def get_dates(db: sqlite3.Connection) -> list[dict]:
    """List available dates with record counts (main DB + archive)."""
    rows = db.execute(
        "SELECT date, COUNT(*) as cnt FROM records GROUP BY date ORDER BY date DESC"
    ).fetchall()
    main_dates = {r["date"] for r in rows}
    results = [{"date": r["date"], "records": r["cnt"], "archived": False} for r in rows]

    for ad in _list_archive_dates():
        if ad["date"] not in main_dates:
            results.append(ad)

    results.sort(key=lambda x: x["date"], reverse=True)
    return results
