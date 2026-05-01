"""Plugin: pod-introspect (Tier 1).

Read-only view of pods/logs/events/metrics in the `tardai` namespace.
Uses the in-pod ServiceAccount token to talk to the k8s API directly —
no kubectl binary required, smaller image.
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
    "SELF_BASE", "http://tool-pod-introspect.tardai.svc.cluster.local:8000"
).rstrip("/")
NAMESPACE = os.environ.get("TARGET_NAMESPACE", "tardai")
K8S_API = "https://kubernetes.default.svc"
SA_TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"
SA_CA_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-pod-introspect", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


def _sa_token() -> str:
    try:
        with open(SA_TOKEN_PATH, "r", encoding="utf-8") as f:
            return f.read().strip()
    except FileNotFoundError:
        raise HTTPException(500, "service account token not mounted")


def _k8s_client() -> httpx.AsyncClient:
    return httpx.AsyncClient(
        base_url=K8S_API,
        headers={"Authorization": f"Bearer {_sa_token()}"},
        verify=SA_CA_PATH if os.path.exists(SA_CA_PATH) else True,
        timeout=10.0,
    )


MANIFEST = {
    "id": "pod-introspect",
    "title": "Read pod state, logs, events in tardai namespace",
    "description": (
        "Returns describe/logs/events/top for pods in the tardai namespace "
        "via the k8s API. Service account scoped to tardai-only RBAC. "
        "Actions: describe, logs, events, top."
    ),
    "schema_in": {
        "action": "describe|logs|events|top",
        "pod": "string (required for describe/logs)",
        "container": "string (optional for logs)",
        "lines": "int (optional, default 100)",
    },
    "schema_out": {"action": "string", "data": "object|array|string"},
    "blast_radius": "read-only-cluster",
    "data_sensitivity": "none",
    "rate_limit_per_min": 30,
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
                print(f"[pod-introspect] registered: {r.status_code}")
                return
            print(f"[pod-introspect] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[pod-introspect] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {"ok": True, "namespace": NAMESPACE}


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json()
    action = body.get("action", "")
    pod = body.get("pod")
    container = body.get("container")
    lines = int(body.get("lines", 100))
    async with _k8s_client() as c:
        if action == "describe":
            if not pod:
                raise HTTPException(400, "pod required for describe")
            r = await c.get(f"/api/v1/namespaces/{NAMESPACE}/pods/{pod}")
            if r.status_code != 200:
                raise HTTPException(r.status_code, r.text[:300])
            p = r.json()
            return JSONResponse({
                "action": "describe",
                "data": {
                    "name": p.get("metadata", {}).get("name"),
                    "phase": p.get("status", {}).get("phase"),
                    "node": p.get("spec", {}).get("nodeName"),
                    "containers": [
                        {
                            "name": cs.get("name"),
                            "ready": cs.get("ready"),
                            "restartCount": cs.get("restartCount"),
                            "image": cs.get("image"),
                            "state": list(cs.get("state", {}).keys()),
                        }
                        for cs in p.get("status", {}).get("containerStatuses", [])
                    ],
                    "conditions": p.get("status", {}).get("conditions", []),
                    "startTime": p.get("status", {}).get("startTime"),
                },
            })
        elif action == "logs":
            if not pod:
                raise HTTPException(400, "pod required for logs")
            params = {"tailLines": str(lines)}
            if container:
                params["container"] = container
            r = await c.get(f"/api/v1/namespaces/{NAMESPACE}/pods/{pod}/log", params=params)
            if r.status_code != 200:
                raise HTTPException(r.status_code, r.text[:300])
            return JSONResponse({"action": "logs", "data": r.text})
        elif action == "events":
            params = {"limit": str(lines)}
            if pod:
                params["fieldSelector"] = f"involvedObject.name={pod}"
            r = await c.get(f"/api/v1/namespaces/{NAMESPACE}/events", params=params)
            if r.status_code != 200:
                raise HTTPException(r.status_code, r.text[:300])
            data = r.json()
            return JSONResponse({
                "action": "events",
                "data": [
                    {
                        "time": e.get("lastTimestamp") or e.get("eventTime"),
                        "type": e.get("type"),
                        "reason": e.get("reason"),
                        "object": f"{e.get('involvedObject',{}).get('kind')}/{e.get('involvedObject',{}).get('name')}",
                        "message": e.get("message"),
                    }
                    for e in data.get("items", [])
                ],
            })
        elif action == "top":
            r = await c.get(f"/apis/metrics.k8s.io/v1beta1/namespaces/{NAMESPACE}/pods")
            if r.status_code != 200:
                return JSONResponse({
                    "action": "top",
                    "data": [],
                    "error": f"metrics-server returned {r.status_code}",
                })
            data = r.json()
            return JSONResponse({
                "action": "top",
                "data": [
                    {
                        "name": item.get("metadata", {}).get("name"),
                        "containers": [
                            {
                                "name": ct.get("name"),
                                "cpu": ct.get("usage", {}).get("cpu"),
                                "memory": ct.get("usage", {}).get("memory"),
                            }
                            for ct in item.get("containers", [])
                        ],
                    }
                    for item in data.get("items", [])
                ],
            })
        else:
            raise HTTPException(400, "action must be one of: describe, logs, events, top")
