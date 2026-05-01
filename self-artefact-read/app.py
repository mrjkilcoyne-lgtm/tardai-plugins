"""Plugin: self-artefact-read.

Tier 0 sense. Proxies GET to the artefact surface so sovereign can read
her own write surface. Bearer-gated. Path traversal hardened (delegated
to the artefact surface, but we also reject obvious traversal upfront).
"""
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
    "http://tool-self-artefact-read.tardai.svc.cluster.local:8000",
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-self-artefact-read", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


def _safe_path(path: str) -> str:
    if not path or path.startswith(("/", "\\")):
        raise HTTPException(400, "path must be relative")
    pure = PurePosixPath(path.replace("\\", "/"))
    if any(p in ("..", "") for p in pure.parts) or pure.is_absolute():
        raise HTTPException(400, "illegal path component")
    return pure.as_posix()


MANIFEST = {
    "id": "self-artefact-read",
    "title": "Read TARDAI's own artefact surface",
    "description": (
        "GET a file from the artefact write surface. Returns body as utf-8 text "
        "(or base64 if encoding=base64). Sovereign's first eye on her own writing."
    ),
    "schema_in": {"path": "string", "encoding": "utf8|base64 (optional, default utf8)"},
    "schema_out": {"body": "string", "bytes": "int", "content_type": "string"},
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
                print(f"[self-artefact-read] registered with bus: {r.status_code}")
                return
            print(f"[self-artefact-read] register attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[self-artefact-read] register attempt {attempt}: {e}")
        await asyncio.sleep(2)
    print("[self-artefact-read] gave up registering after 30 attempts")


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
    path = _safe_path(body.get("path", ""))
    encoding = body.get("encoding", "utf8")
    url = f"{ARTEFACT_BASE}/api/artefacts/{path}"
    async with httpx.AsyncClient(timeout=10.0) as c:
        r = await c.get(url, headers={"Authorization": f"Bearer {BEARER}"})
    if r.status_code != 200:
        raise HTTPException(r.status_code, f"surface returned {r.status_code}")
    content_type = r.headers.get("content-type", "application/octet-stream")
    if encoding == "base64":
        import base64
        body_str = base64.b64encode(r.content).decode("ascii")
    else:
        try:
            body_str = r.content.decode("utf-8")
        except UnicodeDecodeError:
            raise HTTPException(415, "binary content; pass encoding=base64")
    return JSONResponse(
        {"body": body_str, "bytes": len(r.content), "content_type": content_type, "encoding": encoding}
    )
