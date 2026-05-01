"""Plugin: self-artefact-list. Tier 0. Proxies LIST to the artefact surface."""
from __future__ import annotations

import asyncio
import os
from pathlib import PurePosixPath

import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

BEARER = os.environ.get("BEARER", "")
ARTEFACT_BASE = os.environ.get(
    "ARTEFACT_BASE", "http://tardai-artefacts.tardai.svc.cluster.local:8000"
).rstrip("/")
BUS_BASE = os.environ.get(
    "BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"
).rstrip("/")
SELF_BASE = os.environ.get(
    "SELF_BASE",
    "http://tool-self-artefact-list.tardai.svc.cluster.local:8000",
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-self-artefact-list", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


def _safe_prefix(prefix: str) -> str:
    if not prefix:
        return ""
    if prefix.startswith(("/", "\\")):
        raise HTTPException(400, "prefix must be relative")
    pure = PurePosixPath(prefix.replace("\\", "/").rstrip("/"))
    if any(p in ("..", "") for p in pure.parts) or pure.is_absolute():
        raise HTTPException(400, "illegal prefix component")
    return pure.as_posix()


MANIFEST = {
    "id": "self-artefact-list",
    "title": "List TARDAI's own artefact surface",
    "description": "LIST files under a prefix on the artefact surface. Returns array of paths.",
    "schema_in": {"prefix": "string (optional, default empty = root)"},
    "schema_out": {"paths": "string[]", "count": "int"},
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
                print(f"[self-artefact-list] registered: {r.status_code}")
                return
            print(f"[self-artefact-list] attempt {attempt}: {r.status_code}")
        except Exception as e:
            print(f"[self-artefact-list] attempt {attempt}: {e}")
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
    body = await request.json()
    prefix = _safe_prefix(body.get("prefix", ""))
    url = f"{ARTEFACT_BASE}/api/artefacts"
    params = {"prefix": prefix} if prefix else {}
    async with httpx.AsyncClient(timeout=15.0) as c:
        r = await c.get(url, headers={"Authorization": f"Bearer {BEARER}"}, params=params)
    if r.status_code != 200:
        raise HTTPException(r.status_code, f"surface returned {r.status_code}")
    paths = r.json()
    return JSONResponse({"paths": paths, "count": len(paths), "prefix": prefix})
