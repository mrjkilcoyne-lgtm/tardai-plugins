"""Plugin: http-egress (Tier 1 keystone).

Sovereign-controlled outbound HTTP. Allowlist enforced via ConfigMap
`tardai-tool-bus-policy` (key: egress-allowlist; newline-separated hosts).
Falls back to a hardcoded default allowlist if the ConfigMap is absent.

Two-phase: /plan returns the parsed request (creds redacted, body truncated);
/invoke executes after the Bus's confirm gate.
"""
from __future__ import annotations

import asyncio
import os
import time
from urllib.parse import urlparse

import httpx
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

BEARER = os.environ.get("BEARER", "")
BUS_BASE = os.environ.get(
    "BUS_BASE", "http://tardai-tool-bus.tardai.svc.cluster.local:8000"
).rstrip("/")
SELF_BASE = os.environ.get(
    "SELF_BASE", "http://tool-http-egress.tardai.svc.cluster.local:8000"
).rstrip("/")

DEFAULT_ALLOWLIST = {
    "api.github.com",
    "api.stripe.com",
    "api.cal.com",
    "api.vercel.com",
    "api.cloudflare.com",
    "api.civo.com",
    "hooks.slack.com",
    "api.anthropic.com",
    "api.openai.com",
}

POLICY_PATH = "/etc/tardai-policy/egress-allowlist"

if not BEARER:
    raise RuntimeError("BEARER required")

app = FastAPI(title="tool-http-egress", version="0.1.0")


def _check_auth(request: Request) -> None:
    h = request.headers.get("authorization", "")
    if not h.startswith("Bearer ") or h[7:] != BEARER:
        raise HTTPException(401, "bad bearer")


def _load_allowlist() -> set[str]:
    try:
        with open(POLICY_PATH, "r", encoding="utf-8") as f:
            hosts = {line.strip() for line in f if line.strip() and not line.startswith("#")}
        if hosts:
            return hosts
    except FileNotFoundError:
        pass
    return DEFAULT_ALLOWLIST


def _redact_headers(headers: dict) -> dict:
    out = {}
    for k, v in headers.items():
        if k.lower() in ("authorization", "cookie", "x-api-key", "api-key"):
            out[k] = "***REDACTED***"
        else:
            out[k] = v
    return out


def _parse_request(body: dict) -> dict:
    method = (body.get("method") or "GET").upper()
    url = body.get("url", "")
    if not url:
        raise HTTPException(400, "url required")
    parsed = urlparse(url)
    if parsed.scheme not in ("http", "https"):
        raise HTTPException(400, "scheme must be http(s)")
    host = parsed.hostname or ""
    allowlist = _load_allowlist()
    if host not in allowlist:
        raise HTTPException(403, f"host {host!r} not in allowlist")
    headers = body.get("headers") or {}
    payload = body.get("body")
    timeout_ms = int(body.get("timeout_ms", 10000))
    return {
        "method": method,
        "url": url,
        "host": host,
        "headers": headers,
        "body": payload,
        "timeout_ms": timeout_ms,
    }


MANIFEST = {
    "id": "http-egress",
    "title": "Outbound HTTP to sovereign-allowlisted hosts",
    "description": (
        "Issues HTTP requests to hosts on the sovereign-controlled allowlist "
        "(ConfigMap tardai-tool-bus-policy). All non-allowlisted hosts return "
        "403 with audit. Two-phase: /plan shows redacted request; /invoke runs."
    ),
    "schema_in": {
        "method": "GET|POST|PUT|PATCH|DELETE|HEAD",
        "url": "string (must hit allowlisted host)",
        "headers": "object (optional)",
        "body": "string|object (optional)",
        "timeout_ms": "int (optional, default 10000)",
    },
    "schema_out": {
        "status": "int",
        "headers": "object",
        "body": "string",
        "latency_ms": "int",
    },
    "blast_radius": "write-external",
    "data_sensitivity": "none",
    "rate_limit_per_min": 60,
    "estimated_cost": "low",
    "endpoint": f"{SELF_BASE}/invoke",
    "plan_endpoint": f"{SELF_BASE}/plan",
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
                print(f"[http-egress] registered: {r.status_code}")
                return
            print(f"[http-egress] attempt {attempt}: {r.status_code} {r.text[:200]}")
        except Exception as e:
            print(f"[http-egress] attempt {attempt}: {e}")
        await asyncio.sleep(2)


@app.on_event("startup")
async def on_startup() -> None:
    asyncio.create_task(_register_with_bus())


@app.get("/healthz")
async def healthz() -> dict:
    return {"ok": True, "allowlist_size": len(_load_allowlist())}


@app.post("/plan")
async def plan(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json()
    parsed = _parse_request(body)
    body_preview = parsed["body"]
    if isinstance(body_preview, str) and len(body_preview) > 500:
        body_preview = body_preview[:500] + "...[truncated]"
    return JSONResponse(
        {
            "would_do": f"{parsed['method']} {parsed['url']}",
            "host": parsed["host"],
            "headers": _redact_headers(parsed["headers"]),
            "body_preview": body_preview,
            "timeout_ms": parsed["timeout_ms"],
            "allowlist_size": len(_load_allowlist()),
        }
    )


@app.post("/invoke")
async def invoke(request: Request) -> JSONResponse:
    _check_auth(request)
    body = await request.json()
    parsed = _parse_request(body)
    t0 = time.monotonic()
    timeout_s = parsed["timeout_ms"] / 1000.0
    req_kwargs = {"headers": parsed["headers"]}
    if parsed["body"] is not None:
        if isinstance(parsed["body"], (dict, list)):
            req_kwargs["json"] = parsed["body"]
        else:
            req_kwargs["content"] = str(parsed["body"]).encode("utf-8")
    async with httpx.AsyncClient(timeout=timeout_s, follow_redirects=False) as c:
        r = await c.request(parsed["method"], parsed["url"], **req_kwargs)
    latency_ms = int((time.monotonic() - t0) * 1000)
    try:
        body_str = r.text
        if len(body_str) > 100_000:
            body_str = body_str[:100_000] + "...[truncated]"
    except UnicodeDecodeError:
        body_str = f"<binary {len(r.content)} bytes>"
    return JSONResponse(
        {
            "status": r.status_code,
            "headers": dict(r.headers),
            "body": body_str,
            "latency_ms": latency_ms,
        }
    )
