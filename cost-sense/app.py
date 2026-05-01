"""Plugin: cost-sense (Tier 1).

Aggregates spend per source. Sources we attempt:
  - Anthropic (admin token; usually unavailable → honest "unavailable")
  - Civo (CIVO_API_KEY)
  - Vercel (VERCEL_TOKEN)
  - Bus audit log (per-realiser invocation counts grouped by caller_session)

Honesty rule: if a credential is missing, return `unavailable: true` with
reason. Don't fabricate numbers.
"""
from __future__ import annotations

import asyncio
import os
from collections import Counter, defaultdict
from datetime import datetime, timezone, timedelta

import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

BEARER = os.environ.get("BEARER", "")
BUS_BASE = os.environ.get(
    "BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"
).rstrip("/")
ARTEFACT_BASE = os.environ.get(
    "ARTEFACT_BASE", "http://tardai-artefacts.tardai.svc.cluster.local:8000"
).rstrip("/")
SELF_BASE = os.environ.get(
    "SELF_BASE", "http://tool-cost-sense.tardai.svc.cluster.local:8000"
).rstrip("/")

ANTHROPIC_ADMIN = os.environ.get("ANTHROPIC_ADMIN_TOKEN", "")
CIVO_API_KEY = os.environ.get("CIVO_API_KEY", "")
VERCEL_TOKEN = os.environ.get("VERCEL_TOKEN", "")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-cost-sense", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


def _period_bounds(period: str) -> tuple[datetime, datetime]:
    now = datetime.now(timezone.utc)
    if period == "today":
        start = now.replace(hour=0, minute=0, second=0, microsecond=0)
    elif period == "this-week":
        start = (now - timedelta(days=now.weekday())).replace(hour=0, minute=0, second=0, microsecond=0)
    elif period == "this-month":
        start = now.replace(day=1, hour=0, minute=0, second=0, microsecond=0)
    else:
        start = now.replace(hour=0, minute=0, second=0, microsecond=0)
    return start, now


async def _anthropic_usage(period: str) -> dict:
    if not ANTHROPIC_ADMIN:
        return {"source": "anthropic", "unavailable": True, "reason": "ANTHROPIC_ADMIN_TOKEN not set"}
    # Anthropic admin API endpoint shape varies; honest stub.
    return {
        "source": "anthropic",
        "unavailable": True,
        "reason": "Anthropic admin usage API integration not implemented; token present but no canonical endpoint wired",
    }


async def _civo_usage(period: str) -> dict:
    if not CIVO_API_KEY:
        return {"source": "civo", "unavailable": True, "reason": "CIVO_API_KEY not set"}
    try:
        async with httpx.AsyncClient(timeout=10.0) as c:
            r = await c.get(
                "https://api.civo.com/v2/billing",
                headers={"Authorization": f"bearer {CIVO_API_KEY}"},
            )
        if r.status_code == 200:
            return {"source": "civo", "unavailable": False, "data": r.json()}
        return {"source": "civo", "unavailable": True, "reason": f"civo API {r.status_code}"}
    except Exception as e:
        return {"source": "civo", "unavailable": True, "reason": f"civo API error: {e}"}


async def _vercel_usage(period: str) -> dict:
    if not VERCEL_TOKEN:
        return {"source": "vercel", "unavailable": True, "reason": "VERCEL_TOKEN not set"}
    try:
        async with httpx.AsyncClient(timeout=10.0) as c:
            r = await c.get(
                "https://api.vercel.com/v1/usage",
                headers={"Authorization": f"Bearer {VERCEL_TOKEN}"},
            )
        if r.status_code == 200:
            return {"source": "vercel", "unavailable": False, "data": r.json()}
        return {"source": "vercel", "unavailable": True, "reason": f"vercel API {r.status_code}"}
    except Exception as e:
        return {"source": "vercel", "unavailable": True, "reason": f"vercel API error: {e}"}


async def _bus_invocations(period: str) -> dict:
    """Read today's audit log and group by caller_session."""
    start, _now = _period_bounds(period)
    # Audit log is daily JSONL files at _meta/audit/YYYY-MM-DD.jsonl
    days = []
    cur = start.date()
    today = datetime.now(timezone.utc).date()
    while cur <= today:
        days.append(cur.isoformat())
        cur = cur.fromordinal(cur.toordinal() + 1)
    by_caller: dict[str, int] = defaultdict(int)
    by_tool: dict[str, int] = defaultdict(int)
    files_read = []
    files_missing = []
    async with httpx.AsyncClient(timeout=10.0) as c:
        for d in days:
            url = f"{ARTEFACT_BASE}/api/artefacts/_meta/audit/{d}.jsonl"
            try:
                r = await c.get(url, headers={"Authorization": f"Bearer {BEARER}"})
                if r.status_code == 200:
                    files_read.append(d)
                    for line in r.text.splitlines():
                        line = line.strip()
                        if not line:
                            continue
                        try:
                            import json
                            entry = json.loads(line)
                            by_caller[entry.get("caller_session", "unknown")] += 1
                            by_tool[entry.get("tool_id", "unknown")] += 1
                        except (ValueError, KeyError):
                            continue
                else:
                    files_missing.append(d)
            except Exception:
                files_missing.append(d)
    return {
        "source": "bus-audit",
        "unavailable": False,
        "data": {
            "by_caller_session": dict(by_caller),
            "by_tool_id": dict(by_tool),
            "files_read": files_read,
            "files_missing": files_missing,
            "total_invocations": sum(by_caller.values()),
        },
    }


MANIFEST = {
    "id": "cost-sense",
    "title": "Aggregate spend across Anthropic, Civo, Vercel, plus per-realiser invocation counts",
    "description": (
        "Pulls usage from each available source (returns unavailable for any "
        "missing credentials — no fabrication). Also groups Bus audit log "
        "invocations by caller_session for per-realiser allocation."
    ),
    "schema_in": {
        "period": "today|this-week|this-month (optional, default today)",
        "scope": "all|<service-name> (optional, default all)",
    },
    "schema_out": {
        "period": "string",
        "sources": "array",
        "summary": "object",
    },
    "blast_radius": "read-only-external",
    "data_sensitivity": "financial",
    "rate_limit_per_min": 30,
    "estimated_cost": "low",
    "endpoint": f"{SELF_BASE}/invoke",
    "deprecated": False,
    "owner": "claude",
}


async def _register_with_bus() -> None:
    url = f"{BUS_BASE}/api/tools/register"
    for attempt in range(30):
        try:
            async with httpx.AsyncClient(timeout=5.0) as c:
                r = await c.post(
                    url,
                    headers={"Authorization": f"Bearer {BEARER}"},
                    json={"manifest": MANIFEST},
                )
            if r.status_code in (200, 201):
                print(f"[cost-sense] registered: {r.status_code}")
                return
            print(f"[cost-sense] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[cost-sense] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {
        "ok": True,
        "credentials_present": {
            "anthropic": bool(ANTHROPIC_ADMIN),
            "civo": bool(CIVO_API_KEY),
            "vercel": bool(VERCEL_TOKEN),
        },
    }


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json() if (await request.body()) else {}
    period = body.get("period", "today")
    scope = body.get("scope", "all")
    source_calls = []
    if scope in ("all", "anthropic"):
        source_calls.append(_anthropic_usage(period))
    if scope in ("all", "civo"):
        source_calls.append(_civo_usage(period))
    if scope in ("all", "vercel"):
        source_calls.append(_vercel_usage(period))
    if scope in ("all", "bus-audit", "realisers"):
        source_calls.append(_bus_invocations(period))
    sources = await asyncio.gather(*source_calls)
    available = [s for s in sources if not s.get("unavailable")]
    unavailable = [s for s in sources if s.get("unavailable")]
    return JSONResponse({
        "period": period,
        "scope": scope,
        "sources": sources,
        "summary": {
            "available_count": len(available),
            "unavailable_count": len(unavailable),
            "unavailable_reasons": [
                {"source": s["source"], "reason": s["reason"]} for s in unavailable
            ],
        },
    })
