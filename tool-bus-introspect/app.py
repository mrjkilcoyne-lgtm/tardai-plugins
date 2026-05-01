"""Plugin: tool-bus-introspect.

Tier 0 sense. Calls the Bus's own /api/tools/manifest and returns an
enriched view (counts, breakdown by blast_radius, disabled list). Per
Sovereign Amendment A3, this plugin is registered IN the Bus same as any
other plugin — recursion is the feature. Sovereign asks the Bus 'what can
you do?' through a tool the Bus itself dispatches.
"""
from __future__ import annotations

import asyncio
import os
from collections import Counter

import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

BEARER = os.environ.get("BEARER", "")
BUS_BASE = os.environ.get(
    "BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"
).rstrip("/")
SELF_BASE = os.environ.get(
    "SELF_BASE",
    "http://tool-tool-bus-introspect.tardai.svc.cluster.local:8000",
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-tool-bus-introspect", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


MANIFEST = {
    "id": "tool-bus-introspect",
    "title": "Introspect the Tool Bus itself",
    "description": (
        "Returns the Bus's manifest enriched with counts and a breakdown by "
        "blast_radius. Recursion is the feature: this tool is registered in "
        "the Bus and dispatched by the Bus, so sovereign can ask 'what can "
        "you do?' through her own dispatcher."
    ),
    "schema_in": {},
    "schema_out": {
        "tool_count": "int",
        "tools": "array",
        "by_blast_radius": "object",
        "disabled_count": "int",
    },
    "blast_radius": "read-only-cluster",
    "data_sensitivity": "none",
    "rate_limit_per_min": 120,
    "estimated_cost": "zero",
    "endpoint": f"{SELF_BASE}/invoke",
    "deprecated": False,
    "owner": "claude-overnight-agent",
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
                print(f"[tool-bus-introspect] registered: {r.status_code}")
                return
            print(f"[tool-bus-introspect] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[tool-bus-introspect] attempt {attempt}: {e}")
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
    url = f"{BUS_BASE}/api/tools/manifest"
    async with httpx.AsyncClient(timeout=10.0) as c:
        r = await c.get(url, headers={"Authorization": f"Bearer {BEARER}"})
    if r.status_code != 200:
        raise HTTPException(r.status_code, f"bus returned {r.status_code}")
    tools = r.json()
    by_radius = Counter(t.get("blast_radius", "unknown") for t in tools)
    disabled = [t for t in tools if t.get("disabled")]
    return JSONResponse(
        {
            "tool_count": len(tools),
            "tools": [
                {
                    "id": t.get("id"),
                    "title": t.get("title"),
                    "blast_radius": t.get("blast_radius"),
                    "data_sensitivity": t.get("data_sensitivity"),
                    "effective_confirm_tier": t.get("effective_confirm_tier"),
                    "disabled": t.get("disabled", False),
                    "owner": t.get("owner"),
                }
                for t in tools
            ],
            "by_blast_radius": dict(by_radius),
            "disabled_count": len(disabled),
        }
    )
