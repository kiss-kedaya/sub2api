"""Interactive device login; save secrets only to a private state directory."""
import argparse
import json
import os
from pathlib import Path
import secrets
import time

import httpx


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--state-dir", required=True)
    args = parser.parse_args()
    os.umask(0o077)
    state = Path(args.state_dir).resolve()
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    for name in ("sidecar-key", "signing-key"):
        try:
            with (state / name).open('x') as f:
                f.write(secrets.token_urlsafe(48))
        except FileExistsError:
            pass  # Stable signing keys preserve existing tool continuations.
    client_id = "Iv1.b507a08c87ecfe98"
    with httpx.Client(timeout=30, headers={"Accept": "application/json"}) as client:
        response = client.post("https://github.com/login/device/code", json={"client_id": client_id, "scope": "read:user read:org"})
        response.raise_for_status()
        info = response.json()
        print(f"Open {info['verification_uri']} and enter {info['user_code']}", flush=True)
        deadline = time.monotonic() + int(info['expires_in'])
        interval = max(5, int(info.get('interval', 5)))
        while time.monotonic() < deadline:
            time.sleep(interval)
            response = client.post("https://github.com/login/oauth/access_token", json={"client_id": client_id, "device_code": info['device_code'], "grant_type": "urn:ietf:params:oauth:grant-type:device_code"})
            response.raise_for_status()
            result = response.json()
            if result.get('access_token'):
                temporary = state / '.access-token.new'
                fd = os.open(temporary, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
                with os.fdopen(fd, 'w') as f:
                    f.write(result['access_token'])
                temporary.replace(state / 'access-token')
                print('Authorized. Credential stored privately; restart the sidecar to reload.')
                return
            reason = result.get('error')
            if reason == 'slow_down':
                interval += 5
            elif reason != 'authorization_pending':
                raise SystemExit('GitHub authorization was denied or expired')
    raise SystemExit('GitHub device authorization expired')


if __name__ == '__main__':
    main()
