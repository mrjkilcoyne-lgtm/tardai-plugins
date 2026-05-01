"""Plugin: mempalace-read (Tier 1).

Read sovereign's MemPalace memory store at substrates/mempalace:8095.
The MemPalace HTTP API surface is not yet documented — this plugin probes
a list of plausible endpoints and returns whichever responds. Surfaces gap
to STATUS.md when no endpoint matches.
"""
from __future__ import annotations

import asyncio
import os

import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

BEARER = os.environ.get("BEARER", "")
BUS_BASE = os.environ.get(
    "BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"
).rstrip("/")
SELF_BASE = os.environ.get(
    "SELF_BASE", "http://tool-mempalace-read.tardai.svc.cluster.local:8000"
).rstrip("/")
MEMPALACE_URL = os.environ.get(
    "MEMPALACE_URL", "http://mempalace.substrates.svc.cluster.local:8095"
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-mempalace-read", version="0.1.0")

# Plausible read endpoints; first that returns 200 wins.
READ_PATHS = [
    "/api/memories",
    "/api/memory",
    "/memories",
    "/palace/list",
    "/list",
    "/recall",
]


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


MANIFEST = {
    "id": "mempalace-read",
    "title": "Read sovereign's MemPalace memories",
    "description": (
        "List/read memory entries from MemPalace. Optional filters: query "
        "(substring), category, key, limit. Read-only against sibling cluster "
        "service (mempalace.substrates:8095). API surface auto-probed; see "
        "STATUS.md for endpoint discovery state."
    ),
    "schema_in": {
        "query": "string (optional, substring filter)",
        "category": "string (optional)",
        "key": "string (optional, exact-match key)",
        "limit": "int (optional, default 50)",
    },
    "schema_out": {
        "memories": "array",
        "count": "int",
        "endpoint_used": "string",
    },
    "blast_radius": "read-only-external",
    "data_sensitivity": "none",
    "rate_limit_per_min": 60,
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
                print(f"[mempalace-read] registered: {r.status_code}")
                return
            print(f"[mempalace-read] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[mempalace-read] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {"ok": True}


async def _try_read(client: httpx.AsyncClient, params: dict) -> tuple[str, list] | None:
    for p in READ_PATHS:
        try:
            r = await client.get(f"{MEMPALACE_URL}{p}", params=params)
            if r.status_code == 200:
                data = r.json()
                # Normalise: memories might be a list, or {"memories": [...]}, or {"results": [...]}
                if isinstance(data, list):
                    return p, data
                for k in ("memories", "results", "items", "entries"):
                    if isinstance(data, dict) and k in data and isinstance(data[k], list):
                        return p, data[k]
                if isinstance(data, dict):
                    return p, [data]
        except (httpx.HTTPError, ValueError):
            continue
    return None


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json()
    query = body.get("query")
    category = body.get("category")
    key = body.get("key")
    limit = int(body.get("limit", 50))
    params = {}
    if query:
        params["q"] = query
    if category:
        params["category"] = category
    if key:
        params["key"] = key
    params["limit"] = limit
    async with httpx.AsyncClient(timeout=10.0) as c:
        result = await _try_read(c, params)
    if result is None:
        return JSONResponse(
            {
                "memories": [],
                "count": 0,
                "endpoint_used": None,
                "error": "MemPalace API surface not discovered; see STATUS.md",
                "tried_paths": READ_PATHS,
            },
            status_code=502,
        )
    path, memories = result
    # Client-side filtering as fallback
    if query and isinstance(memories, list):
        q = query.lower()
        memories = [
            m for m in memories
            if isinstance(m, dict) and any(q in str(v).lower() for v in m.values())
        ]
    if category and isinstance(memories, list):
        memories = [m for m in memories if isinstance(m, dict) and m.get("category") == category]
    if key and isinstance(memories, list):
        memories = [m for m in memories if isinstance(m, dict) and m.get("key") == key]
    memories = memories[:limit]
    return JSONResponse(
        {
            "memories": memories,
            "count": len(memories),
            "endpoint_used": path,
        }
    )
