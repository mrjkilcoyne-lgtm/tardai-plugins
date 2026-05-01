"""Plugin: mempalace-search (Tier 1).

Semantic search over MemPalace. Probes plausible semantic-search endpoints
on the MemPalace service; falls back to substring + tag scoring against
listing endpoints if no native semantic-search exists.
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
    "SELF_BASE", "http://tool-mempalace-search.tardai.svc.cluster.local:8000"
).rstrip("/")
MEMPALACE_URL = os.environ.get(
    "MEMPALACE_URL", "http://mempalace.substrates.svc.cluster.local:8095"
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-mempalace-search", version="0.1.0")

SEARCH_PATHS = ["/api/search", "/search", "/palace/search", "/api/semantic"]
LIST_PATHS = ["/api/memories", "/memories", "/palace/list"]


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


MANIFEST = {
    "id": "mempalace-search",
    "title": "Semantic search over MemPalace",
    "description": (
        "Ranked search across sovereign's memories. Tries native semantic "
        "search endpoints; falls back to substring + tag matching with score "
        "estimation. See STATUS.md for the current discovery state."
    ),
    "schema_in": {
        "query": "string (required)",
        "limit": "int (optional, default 10)",
        "tags": "array (optional)",
    },
    "schema_out": {
        "results": "array",
        "count": "int",
        "method": "semantic|substring-fallback",
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
                print(f"[mempalace-search] registered: {r.status_code}")
                return
            print(f"[mempalace-search] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[mempalace-search] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {"ok": True}


def _score(memory: dict, query: str, tags: list[str]) -> float:
    q = query.lower()
    score = 0.0
    for v in memory.values() if isinstance(memory, dict) else []:
        s = str(v).lower()
        if q in s:
            score += 1.0
            score += s.count(q) * 0.1
    if tags and isinstance(memory, dict):
        mem_tags = memory.get("tags") or memory.get("category") or []
        if isinstance(mem_tags, str):
            mem_tags = [mem_tags]
        for t in tags:
            if t in mem_tags:
                score += 0.5
    return score


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json()
    query = body.get("query")
    if not query:
        raise HTTPException(400, "query required")
    limit = int(body.get("limit", 10))
    tags = body.get("tags") or []
    async with httpx.AsyncClient(timeout=15.0) as c:
        # Try native semantic first
        for p in SEARCH_PATHS:
            try:
                r = await c.post(f"{MEMPALACE_URL}{p}", json={"query": query, "limit": limit, "tags": tags})
                if r.status_code == 200:
                    data = r.json()
                    results = data if isinstance(data, list) else data.get("results", data.get("memories", []))
                    return JSONResponse({
                        "results": results[:limit],
                        "count": len(results[:limit]),
                        "method": "semantic",
                        "endpoint_used": p,
                    })
            except (httpx.HTTPError, ValueError):
                continue
        # Try GET variant
        for p in SEARCH_PATHS:
            try:
                r = await c.get(f"{MEMPALACE_URL}{p}", params={"q": query, "limit": limit})
                if r.status_code == 200:
                    data = r.json()
                    results = data if isinstance(data, list) else data.get("results", data.get("memories", []))
                    return JSONResponse({
                        "results": results[:limit],
                        "count": len(results[:limit]),
                        "method": "semantic",
                        "endpoint_used": p,
                    })
            except (httpx.HTTPError, ValueError):
                continue
        # Fallback: list + score
        for p in LIST_PATHS:
            try:
                r = await c.get(f"{MEMPALACE_URL}{p}")
                if r.status_code == 200:
                    data = r.json()
                    if isinstance(data, list):
                        memories = data
                    elif isinstance(data, dict):
                        memories = data.get("memories", data.get("results", data.get("items", [])))
                    else:
                        memories = []
                    scored = [(m, _score(m, query, tags)) for m in memories]
                    scored = [(m, s) for m, s in scored if s > 0]
                    scored.sort(key=lambda x: x[1], reverse=True)
                    results = [{"memory": m, "score": s} for m, s in scored[:limit]]
                    return JSONResponse({
                        "results": results,
                        "count": len(results),
                        "method": "substring-fallback",
                        "endpoint_used": p,
                    })
            except (httpx.HTTPError, ValueError):
                continue
    return JSONResponse(
        {
            "results": [],
            "count": 0,
            "method": "none",
            "endpoint_used": None,
            "error": "MemPalace API surface not discovered; see STATUS.md",
        },
        status_code=502,
    )
