import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import types

expected = sys.argv[1]
source = Path('/tmp/rollout322_guard.py')
script = source.read_bytes()
assert hashlib.sha256(script).hexdigest() == expected
guard = types.ModuleType('guard')
exec(compile(script, str(source), 'exec'), guard.__dict__)
assert guard.HOST in guard.CONFIGS
root = guard.ROOT
with open(root/'lock', 'a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    before = guard.get_state()
    assert before['phase'] == 'rolled_back' and before['percent'] == 0 and not before['armed']
    assert 'pending' not in before and guard.fingerprint() == before['config_sha256']
    assert guard.CONFIG.read_text().strip() == guard.rollback_config([ip for ip, _ in guard.SHARES]).strip()
    guard.health(include_old=True)
    sample = guard.metrics()
    assert sample['mem_available_kb'] >= sample['rss_kb_321']+8*1024*1024
    assert guard.run('systemctl', 'is-active', 'sub2api-322-rollout-guard.timer') == 'active'
    assert guard.fingerprint() == before['config_sha256'], 'Configuration changed during preflight'
    backup = root/('resume-backup-'+time.strftime('%Y%m%dT%H%M%SZ', time.gmtime()))
    backup.mkdir(mode=0o700)
    for name in ('rollout322_guard.py', 'state.json', 'pids.json', 'last-check.json'):
        shutil.copy2(root/name, backup/name)
    shutil.copy2(guard.CONFIG, backup/'upstream.before')
    stage = root/'rollout322_guard.py.new'
    stage.write_bytes(script)
    os.chmod(stage, 0o600)
    os.replace(stage, root/'rollout322_guard.py')
    baseline = guard.traffic(time.time()-300)
    _, _, rate = guard.error_rate(baseline.get('business_321', {}))
    now = time.time()
    assert guard.fingerprint() == before['config_sha256'], 'Configuration changed before resume'
    prepared = {'phase': 'ready', 'percent': 0, 'armed': False, 'at': now, 'baseline_at': now,
                'baseline_error_rate': rate, 'rollback_policy': 'platform_health_only',
                'config_sha256': before['config_sha256'], 'resumed_from': str(backup)}
    guard.save(prepared)
    try:
        guard.apply(1, 0)
    except BaseException:
        state = guard.get_state()
        if state == prepared and guard.fingerprint() == before['config_sha256']:
            guard.save(before)
        raise
    print(json.dumps({'host': guard.HOST, 'backup': str(backup), 'guard_sha256': expected,
                      'percent': 1, 'original_pids_preserved': True}), flush=True)
