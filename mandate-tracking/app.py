"""Plugin: mandate-tracking (Tier 1, sovereign's added bonus #3).

Reads `_meta/mandates/<id>.yaml` files from the artefact surface. Each
mandate is a YAML doc with: id, title, opened_at, deadline?, status,
blockers, owner. Read-only. Writes are via the artefact surface POST.
"""
from __future__ import annotations

import asyncio
import os

import httpx
import yaml
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
    "SELF_BASE", "http://tool-mandate-tracking.tardai.svc.cluster.local:8000"
).rstrip("/")

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-mandate-tracking", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


MANIFEST = {
    "id": "mandate-tracking",
    "title": "List sovereign's open / completed / blocked mandates",
    "description": (
        "Reads _meta/mandates/<id>.yaml from the artefact surface. Filters by "
        "status. Returns structured list. Read-only — mandates are written "
        "via the artefact surface POST by sovereign or claude."
    ),
    "schema_in": {
        "status": "open|completed|blocked|all (optional, default all)",
    },
    "schema_out": {
        "mandates": "array",
        "count": "int",
        "by_status": "object",
    },
    "blast_radius": "read-only-cluster",
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
                print(f"[mandate-tracking] registered: {r.status_code}")
                return
            print(f"[mandate-tracking] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[mandate-tracking] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {"ok": True}


async def _list_mandate_files(client: httpx.AsyncClient) -> list[str]:
    url = f"{ARTEFACT_BASE}/api/artefacts/_meta/mandates/"
    headers = {"Authorization": f"Bearer {BEARER}"}
    # Try list endpoint variants
    for params in [{"prefix": "_meta/mandates/"}, {"path": "_meta/mandates"}]:
        try:
            r = await client.get(
                f"{ARTEFACT_BASE}/api/artefacts",
                params=params,
                headers=headers,
            )
            if r.status_code == 200:
                data = r.json()
                if isinstance(data, list):
                    return [p for p in data if p.endswith(".yaml") and "_meta/mandates" in p]
                if isinstance(data, dict):
                    items = data.get("files", data.get("paths", data.get("items", [])))
                    return [
                        (p if isinstance(p, str) else p.get("path", ""))
                        for p in items
                        if (p if isinstance(p, str) else p.get("path", "")).endswith(".yaml")
                        and "_meta/mandates" in (p if isinstance(p, str) else p.get("path", ""))
                    ]
        except (httpx.HTTPError, ValueError):
            continue
    return []


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json() if (await request.body()) else {}
    status_filter = body.get("status", "all")
    headers = {"Authorization": f"Bearer {BEARER}"}
    mandates = []
    async with httpx.AsyncClient(timeout=15.0) as c:
        files = await _list_mandate_files(c)
        for path in files:
            try:
                # Normalise path
                rel = path.lstrip("/")
                if rel.startswith("_meta/"):
                    fetch_path = rel
                else:
                    fetch_path = f"_meta/mandates/{rel.split('/')[-1]}"
                r = await c.get(f"{ARTEFACT_BASE}/api/artefacts/{fetch_path}", headers=headers)
                if r.status_code == 200:
                    try:
                        doc = yaml.safe_load(r.text)
                        if isinstance(doc, dict):
                            mandates.append(doc)
                    except yaml.YAMLError:
                        continue
            except httpx.HTTPError:
                continue
    if status_filter != "all":
        mandates = [m for m in mandates if m.get("status") == status_filter]
    by_status: dict[str, int] = {}
    for m in mandates:
        s = m.get("status", "unknown")
        by_status[s] = by_status.get(s, 0) + 1
    return JSONResponse({
        "mandates": mandates,
        "count": len(mandates),
        "by_status": by_status,
    })
