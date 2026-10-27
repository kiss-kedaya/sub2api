"""Authenticated, tenant-scoped HTTP entrypoint for a pinned ghcp_proxy adapter.

Does not import proxy.py: desktop startup hooks and client configuration writers
must never run in a service. One process owns one GitHub credential/state tree.
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import importlib
import json
import os
import sys
import io
import gzip
import zlib
from contextlib import asynccontextmanager
from contextvars import ContextVar
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

PINNED_REVISION = "ad23ce2db3b5212c0355762d981c3877322fb160"
TENANT = ContextVar("copilot_tenant", default="")
PREFIX = "s2cp1_"
MAX_BODY = 16 * 1024 * 1024


def decode_body(raw, encoding):
    raw = bytes(raw)
    if encoding == 'gzip' or (not encoding and raw.startswith(b'\x1f\x8b')):
        with gzip.GzipFile(fileobj=io.BytesIO(raw)) as source:
            raw = source.read(MAX_BODY + 1)
    elif encoding == 'zstd' or (not encoding and raw.startswith(b'\x28\xb5\x2f\xfd')):
        import zstandard
        try:
            with zstandard.ZstdDecompressor().stream_reader(io.BytesIO(raw)) as source:
                raw = source.read(MAX_BODY + 1)
        except zstandard.ZstdError as exc:
            raise ValueError('Invalid zstd body') from exc
    elif encoding == 'deflate':
        decoder = zlib.decompressobj()
        raw = decoder.decompress(raw, MAX_BODY + 1)
        if not decoder.eof:
            raise ValueError('Oversized or incomplete compressed request')
    elif encoding not in ('', 'identity'):
        raise ValueError('Unsupported content encoding')
    if len(raw) > MAX_BODY:
        raise ValueError('Decompressed request exceeds 16 MiB')
    return json.loads(raw)


def error(status, message):
    return JSONResponse({"error": {"type": "invalid_request_error", "message": message}}, status_code=status)


class ContinuationCodec:
    def __init__(self, secret: bytes):
        self.secret = secret

    def encode(self, raw: str, tenant: str) -> str:
        payload = base64.urlsafe_b64encode(raw.encode()).decode().rstrip("=")
        tag = hmac.new(self.secret, (tenant + ":" + payload).encode(), hashlib.sha256).hexdigest()
        return PREFIX + payload + "." + tag

    def decode(self, value: str, tenant: str) -> str:
        if not isinstance(value, str) or not value.startswith(PREFIX) or len(value) > 8192:
            raise ValueError("Invalid Copilot continuation")
        payload, tag = value[len(PREFIX):].rsplit(".", 1)
        expected = hmac.new(self.secret, (tenant + ":" + payload).encode(), hashlib.sha256).hexdigest()
        if not hmac.compare_digest(expected.encode(), tag.encode()):
            raise ValueError("Continuation belongs to another client or sidecar")
        return base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)).decode()


def create_app(sdk, translation, *, bearer: str, signing_key: bytes, require_client_id=True):
    if len(bearer) < 32 or len(signing_key) < 32:
        raise ValueError("Sidecar bearer and signing key must each contain at least 32 bytes")
    codec = ContinuationCodec(signing_key)
    original_encode, original_decode = sdk._encode_call_id, sdk._decode_call_id

    def encode(*args, **kwargs):
        return codec.encode(original_encode(*args, **kwargs), TENANT.get())

    def decode(value):
        try:
            return original_decode(codec.decode(value, TENANT.get()))
        except (ValueError, UnicodeError):
            return None

    sdk._encode_call_id, sdk._decode_call_id = encode, decode
    # Caller cwd belongs to the Codex machine, never the service host.
    sdk._workspace_from_request = lambda body: None
    active = set()

    @asynccontextmanager
    async def lifespan(app):
        yield
        await sdk.shutdown()

    app = FastAPI(lifespan=lifespan, docs_url=None, redoc_url=None, openapi_url=None)

    @app.middleware("http")
    async def guard(request, call_next):
        if request.url.path == "/healthz":
            return await call_next(request)
        supplied = request.headers.get("authorization", "")
        if not hmac.compare_digest(supplied.encode(), ("Bearer " + bearer).encode()):
            return error(401, "Invalid sidecar API key")
        client_id = request.headers.get("x-sub2api-client-id", "")
        if require_client_id and (not client_id.isdecimal() or int(client_id) <= 0):
            return error(400, "A trusted Sub2API client identity is required")
        request.state.tenant = client_id or "direct"
        return await call_next(request)

    @app.get("/healthz")
    async def health():
        return {"status": "ok"}

    @app.get("/v1/models")
    async def models():
        try:
            client = await sdk._get_client()
            values = await client.list_models()
            return {"object": "list", "data": [{"id": m.id, "object": "model", "owned_by": "github-copilot"} for m in values]}
        except Exception:
            return error(502, "Copilot model discovery failed; check service authentication")

    async def responses(request: Request):
        try:
            raw = bytearray()
            async for chunk in request.stream():
                raw.extend(chunk)
                if len(raw) > MAX_BODY:
                    return error(413, "Request exceeds 16 MiB")
            body = decode_body(raw, request.headers.get('content-encoding', '').lower().strip())
            if not isinstance(body, dict) or not isinstance(body.get("model"), str):
                return error(400, "A Responses object with a model is required")
        except (ValueError, UnicodeError, OSError, EOFError, zlib.error):
            return error(400, "Invalid JSON request")
        tenant = request.state.tenant
        token = TENANT.set(tenant)
        try:
            # Resolve identity before overwriting the explicit session field.
            thread_id = getattr(sdk, "_client_thread_id", lambda _: None)(body)
            identity = thread_id or sdk._session_alias(body) or request.headers.get("session_id") or body.get("prompt_cache_key")
            if not isinstance(identity, str) or not identity.strip():
                return error(400, "A stable Codex session/thread ID is required")
            scoped = hmac.new(signing_key, (tenant + ":" + identity).encode(), hashlib.sha256).hexdigest()
            continuation_scope = tenant + ":" + scoped
            TENANT.set(continuation_scope)
            body["session_id"] = scoped
            body["prompt_cache_key"] = scoped
            items = body.get("input")
            for item in items if isinstance(items, list) else []:
                if not isinstance(item, dict):
                    continue
                if item.get("type") in {"function_call", "custom_tool_call", "function_call_output", "custom_tool_call_output"}:
                    call_id = item.get("call_id")
                    decoded = decode(call_id)
                    if decoded is None:
                        return error(409, "Tool history is not owned by this sidecar/client; start a new thread")
                    if not sdk._owns_session(decoded["s"]):
                        return error(409, "Copilot session is unavailable on this instance; do not retry on another account")
            continuation = sdk.resolve_tool_continuation(items)
            if continuation:
                entry = sdk._live_sessions.get(continuation[0])
                if entry is None or not entry.pending_calls:
                    return error(409, "Pending SDK turn is no longer live; start a new thread instead of replaying tool results")
            if scoped in active:
                return error(409, "This Copilot thread already has an active request")
            active.add(scoped)
            try:
                compact = request.url.path.endswith("/compact") or sdk.is_compaction_request(body)
                if compact:
                    body = translation.build_fake_compaction_request(body)
                    body["session_id"] = scoped
                    body["prompt_cache_key"] = scoped
                    body["tool_choice"] = "none"
                response = await sdk.handle_responses(request, body, is_compact=compact)
                if hasattr(response, "body_iterator"):
                    original = response.body_iterator

                    async def stream():
                        context = TENANT.set(continuation_scope)
                        try:
                            async for chunk in original:
                                yield chunk
                        finally:
                            try:
                                await original.aclose()
                            finally:
                                active.discard(scoped)
                                TENANT.reset(context)

                    response.body_iterator = stream()
                else:
                    active.discard(scoped)
                return response
            except Exception:
                active.discard(scoped)
                return error(502, "Copilot SDK request failed")
        finally:
            TENANT.reset(token)

    app.add_api_route("/v1/responses", responses, methods=["POST"])
    app.add_api_route("/v1/responses/compact", responses, methods=["POST"])
    return app


def load_app():
    source = Path(os.environ["COPILOT_ADAPTER_DIR"]).resolve()
    import subprocess
    revision = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    if revision != PINNED_REVISION:
        raise RuntimeError("Copilot adapter revision differs from the validated pin")
    if subprocess.check_output(["git", "-C", str(source), "status", "--porcelain", "--untracked-files=no"], text=True).strip():
        raise RuntimeError("Copilot adapter tracked files have local modifications")
    sys.path.insert(0, str(source))
    state = Path(os.environ["GHCP_STATE_DIR"])
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not (state / 'access-token').is_file() or not (state / 'access-token').read_text().strip():
        raise RuntimeError('Run the sidecar device login before starting the service')
    from importlib.metadata import version
    if version('github-copilot-sdk') != '1.0.14':
        raise RuntimeError('This sidecar requires github-copilot-sdk 1.0.14')
    for key in ("GHCP_CONFIG_DIR", "GHCP_CACHE_DIR"):
        os.environ.setdefault(key, str(state / key.lower()))
    sdk = importlib.import_module("copilot_sdk_upstream")
    sdk._client_thread_id = importlib.import_module("codex_agent_compat").codex_thread_id
    translation = importlib.import_module("format_translation")
    return create_app(sdk, translation,
                      bearer=Path(os.environ["COPILOT_SIDECAR_KEY_FILE"]).read_text().strip(),
                      signing_key=Path(os.environ["COPILOT_SIGNING_KEY_FILE"]).read_bytes().strip(),
                      require_client_id=os.environ.get("COPILOT_ALLOW_DIRECT", "0") != "1")


if __name__ == "__main__":
    import uvicorn
    os.umask(0o077)
    uvicorn.run(load_app(), host="127.0.0.1", port=int(os.environ.get("COPILOT_SIDECAR_PORT", "18765")), access_log=False)
