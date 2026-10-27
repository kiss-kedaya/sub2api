"""Run the actual pinned adapter with SDK events, without contacting GitHub.

Run from the adapter virtualenv with PYTHONPATH pointing to its checkout.
"""
import importlib.util
from pathlib import Path
from types import SimpleNamespace

import httpx
import pytest
from copilot.session_events import AssistantMessageData, ExternalToolRequestedData, SessionIdleData
import copilot_sdk_upstream as sdk
import format_translation

spec = importlib.util.spec_from_file_location('sidecar_contract', Path(__file__).with_name('server.py'))
server = importlib.util.module_from_spec(spec)
spec.loader.exec_module(server)


@pytest.mark.asyncio
async def test_actual_adapter_signed_custom_tool_roundtrip(monkeypatch, tmp_path):
    results = []

    class Session:
        session_id = 'contract-session'
        def __init__(self):
            self.handlers = []
            self.rpc = SimpleNamespace(tools=SimpleNamespace(handle_pending_tool_call=self.result))
        def on(self, handler):
            self.handlers.append(handler)
            return lambda: self.handlers.remove(handler)
        def emit(self, name, data):
            for h in list(self.handlers):
                h(SimpleNamespace(type=SimpleNamespace(value=name), data=data, agent_id=None))
        async def send(self, prompt, **kwargs):
            self.emit('external_tool.requested', ExternalToolRequestedData(request_id='request-1', session_id=self.session_id, tool_call_id='runtime-1', tool_name='ghcp_custom_apply_patch', arguments={'input': '*** Begin Patch\n*** End Patch'}))
        async def result(self, req):
            results.append(req.request_id)
            self.emit('assistant.message', AssistantMessageData(content='verified', message_id='msg-1', phase='final_answer'))
            self.emit('session.idle', SessionIdleData())
            return SimpleNamespace(success=True)
        async def disconnect(self): pass
        async def abort(self): self.emit('session.idle', SessionIdleData(aborted=True))

    session = Session()
    class Client:
        async def create_session(self, **kwargs): return session
        async def resume_session(self, *args, **kwargs): raise AssertionError('must reuse live SDK session')
        async def list_models(self): return []
    async def get_client(): return Client()
    monkeypatch.setattr(sdk, '_get_client', get_client)
    monkeypatch.setattr(sdk, '_SDK_STATE_DIR', str(tmp_path))
    monkeypatch.setattr(sdk, '_live_sessions', {})
    # Record the original hooks so monkeypatch restores create_app's wrappers.
    for name in ('_encode_call_id', '_decode_call_id', '_workspace_from_request'):
        monkeypatch.setattr(sdk, name, getattr(sdk, name))
    app = server.create_app(sdk, format_translation, bearer='k'*40, signing_key=b's'*40)
    body = {'model': 'auto', 'stream': True, 'session_id': 'thread', 'input': [{'role': 'user', 'content': 'patch'}], 'tools': [{'type': 'custom', 'name': 'apply_patch'}]}
    headers = {'Authorization': 'Bearer ' + 'k'*40, 'X-Sub2API-Client-ID': '42'}
    try:
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url='http://test') as c:
            first = await c.post('/v1/responses', headers=headers, json=body)
            import json
            events = [json.loads(line[6:]) for line in first.text.splitlines() if line.startswith('data: ')]
            completed = next(e['response'] for e in events if e['type'] == 'response.completed')
            call = completed['output'][0]
            assert call['type'] == 'custom_tool_call'
            assert call['call_id'].startswith(server.PREFIX)
            assert sdk._live_sessions['contract-session'].pending_calls
            body['input'].extend([call, {'type': 'custom_tool_call_output', 'call_id': call['call_id'], 'output': 'patched'}])
            second = await c.post('/v1/responses', headers=headers, json=body)
            assert second.status_code == 200
            assert 'verified' in second.text
            assert results == ['request-1']
    finally:
        await sdk._evict_all_live_sessions()
