"""Plugin: time-sense (Tier 1).

Wall-clock UTC, monotonic clock, optional delta vs caller-supplied
reference timestamp. Stateless — sovereign passes session-start /
mandate-start timestamps via `relative_to`.
"""
from __future__ import annotations

import asyncio
import os
import time as _time
from datetime import datetime, timezone

import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

BEARER = os.environ.get("BEARER", "")
BUS_BASE = os.environ.get(
    "BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"
).rstrip("/")
SELF_BASE = os.environ.get(
    "SELF_BASE", "http://tool-time-sense.tardai.svc.cluster.local:8000"
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-time-sense", version="0.1.0")
_BOOT_MONOTONIC = _time.monotonic()
_BOOT_WALL = datetime.now(timezone.utc).isoformat()


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


MANIFEST = {
    "id": "time-sense",
    "title": "Wall-clock and monotonic time, with optional delta",
    "description": (
        "Returns current UTC wall-clock and a monotonic counter. If "
        "relative_to (ISO timestamp) is supplied, also returns delta in "
        "seconds. Cheap; intended for sovereign to ground herself in time."
    ),
    "schema_in": {
        "format": "iso|epoch|human (optional, default iso)",
        "relative_to": "ISO timestamp (optional)",
    },
    "schema_out": {
        "now_iso": "string",
        "now_epoch": "float",
        "now_human": "string",
        "monotonic": "float",
        "uptime_s": "float",
        "boot_wall": "string",
        "delta_s": "float|null",
    },
    "blast_radius": "read-only-cluster",
    "data_sensitivity": "none",
    "rate_limit_per_min": 600,
    "estimated_cost": "zero",
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
                print(f"[time-sense] registered: {r.status_code}")
                return
            print(f"[time-sense] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[time-sense] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {"ok": True}


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json() if (await request.body()) else {}
    relative_to = body.get("relative_to")
    now = datetime.now(timezone.utc)
    delta_s = None
    if relative_to:
        try:
            ref = datetime.fromisoformat(relative_to.replace("Z", "+00:00"))
            if ref.tzinfo is None:
                ref = ref.replace(tzinfo=timezone.utc)
            delta_s = (now - ref).total_seconds()
        except ValueError:
            raise HTTPException(400, f"relative_to not ISO: {relative_to!r}")
    return JSONResponse({
        "now_iso": now.isoformat(),
        "now_epoch": now.timestamp(),
        "now_human": now.strftime("%Y-%m-%d %H:%M:%S UTC"),
        "monotonic": _time.monotonic(),
        "uptime_s": _time.monotonic() - _BOOT_MONOTONIC,
        "boot_wall": _BOOT_WALL,
        "delta_s": delta_s,
    })
