import asyncio
import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace

import httpx
import pytest
from fastapi.responses import JSONResponse, StreamingResponse

spec = importlib.util.spec_from_file_location("sidecar_server", Path(__file__).with_name("server.py"))
server = importlib.util.module_from_spec(spec)
spec.loader.exec_module(server)
KEY = "k" * 40
SIGN = b"s" * 40


class SDK:
    def __init__(self):
        self.bodies = []
        self._live_sessions = {'owned': SimpleNamespace(pending_calls=True)}

    def _encode_call_id(self, session_id, request_id, **kwargs):
        return json.dumps({"s": session_id, "r": request_id, **kwargs})

    def _decode_call_id(self, value):
        return json.loads(value)

    def _session_alias(self, body):
        return body.get("session_id") or body.get("client_metadata", {}).get("thread_id")

    def _owns_session(self, session):
        return session == "owned"

    def is_compaction_request(self, body):
        return False

    def resolve_tool_continuation(self, items):
        if isinstance(items, list) and items and items[-1].get('type') in {'function_call_output', 'custom_tool_call_output'}:
            decoded = self._decode_call_id(items[-1]['call_id'])
            return decoded['s'], []
        return None

    async def handle_responses(self, request, body, **kwargs):
        self.bodies.append(body)

        def item():
            return {"type": "custom_tool_call", "call_id": self._encode_call_id("owned", "r1"), "name": "apply_patch", "input": "patch"}

        if body.get("stream"):
            async def events():
                yield 'data: ' + json.dumps(item()) + '\n\n'
            return StreamingResponse(events(), media_type="text/event-stream")
        return JSONResponse({"output": [item()]})

    async def shutdown(self):
        pass


def client(sdk=None, **kwargs):
    sdk = sdk or SDK()
    translation = SimpleNamespace(build_fake_compaction_request=lambda b: dict(b))
    app = server.create_app(sdk, translation, bearer=KEY, signing_key=SIGN, **kwargs)
    return sdk, httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://test")


def headers(tenant="1"):
    return {"Authorization": "Bearer " + KEY, "X-Sub2API-Client-ID": tenant}


def body(**kwargs):
    return {"model": "auto", "session_id": "same-thread", "input": "test", **kwargs}


@pytest.mark.asyncio
async def test_auth_and_client_identity_fail_closed():
    sdk, c = client()
    async with c:
        assert (await c.get('/healthz')).status_code == 200
        assert (await c.post('/v1/responses', json=body())).status_code == 401
        assert (await c.post('/v1/responses', headers=headers('0'), json=body())).status_code == 400
        assert not sdk.bodies


@pytest.mark.asyncio
async def test_tenant_bound_continuation_and_scoped_thread():
    sdk, c = client()
    async with c:
        r = await c.post('/v1/responses', headers=headers(), json=body())
        call = r.json()['output'][0]['call_id']
        continuation = body(input=[{'type': 'custom_tool_call_output', 'call_id': call, 'output': 'ok'}])
        assert (await c.post('/v1/responses', headers=headers(), json=continuation)).status_code == 200
        first = sdk.bodies[0]['session_id']
        assert sdk.bodies[1]['session_id'] == first
        assert (await c.post('/v1/responses', headers=headers('2'), json=continuation)).status_code == 409
        assert (await c.post('/v1/responses', headers=headers(), json={**continuation, 'session_id': 'other-thread'})).status_code == 409
        assert (await c.post('/v1/responses', headers=headers('2'), json=body())).status_code == 200
        assert sdk.bodies[-1]['session_id'] != first


@pytest.mark.asyncio
async def test_stream_signs_call_and_can_resume():
    sdk, c = client()
    async with c:
        r = await c.post('/v1/responses', headers=headers(), json=body(stream=True))
        event = json.loads(r.text.removeprefix('data: ').strip())
        assert event['call_id'].startswith(server.PREFIX)
        r = await c.post('/v1/responses', headers=headers(), json=body(input=[{'type': 'custom_tool_call_output', 'call_id': event['call_id'], 'output': 'done'}]))
        assert r.status_code == 200


@pytest.mark.asyncio
async def test_missing_or_foreign_session_rejected_before_sdk():
    sdk, c = client()
    async with c:
        missing = body(); missing.pop('session_id')
        assert (await c.post('/v1/responses', headers=headers(), json=missing)).status_code == 400
        call = server.ContinuationCodec(SIGN).encode(json.dumps({'s': 'lost', 'r': 'r'}), '1')
        assert (await c.post('/v1/responses', headers=headers(), json=body(input=[{'type': 'function_call_output', 'call_id': call, 'output': 'x'}]))).status_code == 409
        assert not sdk.bodies


@pytest.mark.asyncio
async def test_compact_disables_tools_and_preserves_namespace():
    sdk, c = client()
    tools = [{'type': 'namespace', 'name': 'fs', 'tools': [{'type': 'custom', 'name': 'patch'}]}]
    async with c:
        assert (await c.post('/v1/responses/compact', headers=headers(), json=body(tools=tools))).status_code == 200
        assert sdk.bodies[-1]['tool_choice'] == 'none'
        assert sdk.bodies[-1]['tools'] == tools


@pytest.mark.asyncio
async def test_concurrent_same_thread_rejected():
    sdk = SDK()
    started, release = asyncio.Event(), asyncio.Event()
    original = sdk.handle_responses
    async def blocking(*args, **kwargs):
        started.set()
        await release.wait()
        return await original(*args, **kwargs)
    sdk.handle_responses = blocking
    _, c = client(sdk)
    async with c:
        first = asyncio.create_task(c.post('/v1/responses', headers=headers(), json=body()))
        await started.wait()
        try:
            assert (await c.post('/v1/responses', headers=headers(), json=body())).status_code == 409
        finally:
            release.set()
            assert (await first).status_code == 200


def test_signature_tampering_and_key_rotation():
    codec = server.ContinuationCodec(SIGN)
    value = codec.encode('original', '1')
    with pytest.raises(ValueError): codec.decode(value + 'x', '1')
    with pytest.raises(ValueError): server.ContinuationCodec(b'x' * 40).decode(value, '1')


@pytest.mark.asyncio
async def test_thread_metadata_wins_over_parent_session():
    sdk = SDK()
    sdk._client_thread_id = lambda b: b.get('client_metadata', {}).get('thread_id')
    _, c = client(sdk)
    async with c:
        for thread in ('child-A', 'child-B'):
            r = await c.post('/v1/responses', headers=headers(), json=body(client_metadata={'thread_id': thread}))
            assert r.status_code == 200
        assert sdk.bodies[0]['session_id'] != sdk.bodies[1]['session_id']


@pytest.mark.asyncio
async def test_restarted_pending_turn_rejected():
    sdk, c = client()
    async with c:
        r = await c.post('/v1/responses', headers=headers(), json=body())
        call = r.json()['output'][0]['call_id']
        sdk._live_sessions.clear()
        r = await c.post('/v1/responses', headers=headers(), json=body(input=[{'type': 'custom_tool_call_output', 'call_id': call, 'output': 'done'}]))
        assert r.status_code == 409
        assert len(sdk.bodies) == 1


@pytest.mark.asyncio
async def test_compressed_codex_request():
    import gzip
    import zstandard
    _, c = client()
    async with c:
        raw = json.dumps(body()).encode()
        for encoding, compressed in [('gzip', gzip.compress(raw)), ('zstd', zstandard.ZstdCompressor().compress(raw))]:
            r = await c.post('/v1/responses', headers={**headers(), 'Content-Encoding': encoding}, content=compressed)
            assert r.status_code == 200


def test_compression_limit_is_applied_after_decoding(monkeypatch):
    import gzip
    monkeypatch.setattr(server, 'MAX_BODY', 20)
    with pytest.raises(ValueError):
        server.decode_body(gzip.compress(b'x' * 1000), 'gzip')
