import argparse
import collections
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import time

HOST = socket.gethostname().split('.')[0]
ROOT = Path('/opt/sub2api/rollout-327-20260917')
CONFIGS = {
    'ns572368': ('/etc/nginx/conf.d/sub2api-upstream.conf', 'sub2api_prod'),
    'ns575199': ('/etc/nginx/conf.d/upstream_kedaya_sub2.conf', 'kedaya_sub2'),
}
CONFIG, NAME = CONFIGS.get(HOST, ('/nonexistent', 'test'))
CONFIG = Path(CONFIG)
SHARES = [('10.254.0.2', 40), ('10.254.0.1', 60)]
STABLE_VERSION = '326' if HOST == 'ns572368' else '326-grok600'
STABLE_PORT = 8095 if HOST == 'ns572368' else 8097
INITIAL_BACKUP_PORT = None if HOST == 'ns572368' else 8095


def run(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=45).strip()


def config_for(pct):
    if pct not in (0, 1, 100):
        raise ValueError('Unsupported traffic percentage')
    lines = [f'upstream {NAME} {{']
    for port, weight in [(STABLE_PORT, 100-pct), (8099, pct)]:
        if weight:
            for ip, share in SHARES:
                lines.append(f'    server {ip}:{port} weight={weight*share} max_fails=3 fail_timeout=10s;')
    backup_port = STABLE_PORT if pct == 100 else INITIAL_BACKUP_PORT
    if backup_port:
        for ip, share in SHARES:
            lines.append(f'    server {ip}:{backup_port} weight={share} max_fails=3 fail_timeout=10s backup;')
    return '\n'.join(lines + ['}', ''])


def atomic_text(target, value):
    stage = target.with_name(target.name+'.327-tmp')
    stage.write_text(value)
    os.chmod(stage, 0o644 if target == CONFIG else 0o600)
    os.replace(stage, target)


def save(state):
    atomic_text(ROOT/'state.json', json.dumps(state))


def get_state():
    return json.loads((ROOT/'state.json').read_text())


def fingerprint():
    return hashlib.sha256(CONFIG.read_bytes()).hexdigest()


def health(include_old=False):
    origins = [('https://kedaya.ai/readyz', 'ready', True)]
    for ip, _ in SHARES:
        for port in ((STABLE_PORT, 8099) if include_old else (8099,)):
            origins.append((f'http://{ip}:{port}/readyz', 'ready', False))

    def probe(url, expected, local):
        args = ['curl', '-fsS', '-m', '5', '-H', 'Cache-Control: no-cache']
        if local:
            args += ['--resolve', 'kedaya.ai:443:127.0.0.1', '-k']
        if json.loads(run(*args, url)).get('status') != expected:
            raise RuntimeError('Unexpected health response: '+url)

    for target in origins:
        probe(*target)
    for url, expected in [('https://kedaya.ai/health', 'ok'), ('https://kedaya.ai/readyz', 'ready')]:
        try:
            probe(url, expected, False)
        except subprocess.CalledProcessError as exc:
            # A single transport timeout may precede the origin; confirm it in this check.
            if exc.returncode != 28:
                raise
            atomic_text(ROOT/'last-public-timeout.json', json.dumps({'at': time.time(), 'url': url, 'exit': exc.returncode}))
            for target in origins:
                probe(*target)
            probe(url, expected, False)
    for version, pid in json.loads((ROOT/'pids.json').read_text()).items():
        if version == STABLE_VERSION and not include_old:
            continue
        unit = f'sub2api-0.1.{version}-canary'
        if int(run('systemctl', 'show', unit, '-p', 'MainPID', '--value')) != pid:
            raise RuntimeError('Application PID changed: '+version)
        if run('systemctl', 'is-active', unit) != 'active':
            raise RuntimeError('Application inactive: '+version)


def metrics():
    result = {'host': HOST, 'utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    for version, pid in json.loads((ROOT/'pids.json').read_text()).items():
        try:
            status = Path(f'/proc/{pid}/status').read_text()
        except FileNotFoundError:
            if version != STABLE_VERSION:
                raise
            result['rss_kb_'+STABLE_VERSION] = 0
            result['original_stable_pid_missing'] = True
            continue
        result['rss_kb_'+version] = int(next(l.split()[1] for l in status.splitlines() if l.startswith('VmRSS:')))
    result['load'] = Path('/proc/loadavg').read_text().split()[:3]
    result['mem_available_kb'] = int(next(l.split()[1] for l in Path('/proc/meminfo').read_text().splitlines() if l.startswith('MemAvailable:')))
    return result


def traffic(cutoff, started_after=None):
    paths = [Path('/var/log/nginx/sub2api-rollout.log')]
    if not paths[0].exists():
        paths = [Path('/var/log/nginx/kedaya-ai_access.log'), Path('/var/log/nginx/kedaya-sub2-ip_access.log')]
    counts = collections.defaultdict(collections.Counter)
    for path in paths:
        if not path.exists():
            continue
        with path.open() as stream:
            offset = max(0, os.path.getsize(path)-20000000)
            stream.seek(offset)
            if offset:
                stream.readline()
            for line in stream:
                try:
                    if line.startswith('{'):
                        row = json.loads(line)
                        stamp = datetime.datetime.fromisoformat(row['ts']).timestamp()
                        elapsed = float(row['request_time'])
                        upstream = row['upstream']
                        code = str(row['status'])
                        probe = bool(row.get('probe'))
                    else:
                        stamp = datetime.datetime.strptime(re.search(r'\[([^]]+)\]', line)[1], '%d/%b/%Y:%H:%M:%S %z').timestamp()
                        code = re.search(r'HTTP/\S+" (\d{3})', line)[1]
                        upstream = re.search(r'upstream_addr=(.*?) upstream_status=', line)[1]
                        elapsed = float(re.search(r'(?:request_time|rt)=([0-9.]+)', line)[1])
                        probe = 'GET /health ' in line or 'GET /readyz ' in line
                    if stamp < cutoff or (started_after is not None and stamp-elapsed < started_after):
                        continue
                    ports = re.findall(r':(8095|8097|8099)\b', upstream)
                    if not ports:
                        continue
                    # Attribute retried requests to their final application, not both versions.
                    version = {'8095': '326', '8097': '326-grok600', '8099': '327'}[ports[-1]]
                    counts[('probe_' if probe else 'business_')+version][code] += 1
                except (KeyError, ValueError, TypeError, IndexError):
                    continue
    return dict(counts)


def error_rate(counts):
    total = sum(counts.values())
    errors = sum(v for k, v in counts.items() if k.startswith('5'))
    return total, errors, errors/total if total else 0


def find_platform_faults(raw):
    signals = collections.Counter()
    events = {'openai.responses_panic_recovered', 'openai.messages_panic_recovered',
              'openai.usage_record_task_panic_recovered', 'gateway.usage_record_task_panic_recovered',
              'image_task.execution_panicked'}
    for line in raw.splitlines():
        if line.startswith(('panic:', 'fatal error:')):
            signals['runtime_panic'] += 1
            continue
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if not isinstance(row, dict):
            continue
        message = row.get('msg')
        if message in events:
            signals[message] += 1
    return dict(signals)


def platform_faults(state):
    since = max(state.get('candidate_at', state['at']), state.get('last_check_at', state['at'])-10)
    raw = run('journalctl', '-u', 'sub2api-0.1.327-canary', '--since', '@'+str(int(since)),
              '--no-pager', '-o', 'cat', '-n', '5000')
    return find_platform_faults(raw)


def available_backups():
    available = []
    for ip, _ in SHARES:
        try:
            if json.loads(run('curl', '-fsS', '-m', '3', f'http://{ip}:{STABLE_PORT}/readyz')).get('status') == 'ready':
                available.append(ip)
        except Exception:
            pass
    return available


def rollback_config(available):
    return '\n'.join(line for line in config_for(0).splitlines()
                     if ' backup;' not in line and
                     (f':{STABLE_PORT} ' not in line or any(f'{ip}:{STABLE_PORT} ' in line for ip in available)))+'\n'


def recover_pending():
    state = get_state()
    pending = state.get('pending')
    if pending is None:
        return
    current = CONFIG.read_text()
    if current not in [pending['before'], pending['after'], *pending.get('owned_configs', [])]:
        state.update(phase='disarmed', armed=False, reason='External configuration replaced pending transaction')
        state.pop('pending')
        save(state)
        raise RuntimeError('Pending transaction lost configuration ownership')
    if pending['final_state']['phase'] == 'rolled_back':
        available = available_backups()
        if not available:
            atomic_text(CONFIG, pending['before'])
            run('nginx', '-t')
            run('systemctl', 'reload', 'nginx')
            state.update(phase='rollback_blocked', armed=True, reason='No healthy 326-grok600 target during recovery')
            save(state)
            raise RuntimeError('No healthy 326-grok600 target during recovery; previous route restored')
        after = rollback_config(available)
        if after != pending['after']:
            pending['owned_configs'] = list(set([*pending.get('owned_configs', []), pending['after']]))
            pending['after'] = after
            save(state)
    atomic_text(CONFIG, pending['after'])
    try:
        run('nginx', '-t')
        run('systemctl', 'reload', 'nginx')
    except Exception:
        atomic_text(CONFIG, pending['before'])
        raise
    final_state = pending['final_state']
    if final_state['phase'] == 'watching':
        final_state.update(at=time.time(), successful_checks=0)
        if final_state['percent'] == 1:
            final_state['candidate_at'] = final_state['at']
    elif final_state['phase'] == 'rolled_back':
        final_state['rollback_at'] = time.time()
    save({**final_state, 'config_sha256': fingerprint()})


def transition(after, final_state):
    state = get_state()
    if fingerprint() != state['config_sha256']:
        raise RuntimeError('Configuration ownership lost before transaction')
    # Persist both sides before changing the file. A killed process can resume its own reload.
    state['pending'] = {'before': CONFIG.read_text(), 'after': after, 'final_state': final_state}
    save(state)
    recover_pending()


def rollback(reason):
    state = get_state()
    if fingerprint() != state['config_sha256']:
        state.update(phase='disarmed', reason='Configuration changed externally', armed=False)
        save(state)
        raise RuntimeError('Configuration ownership lost; refusing stale rollback')
    before = CONFIG.read_text()
    atomic_text(ROOT/f'rollback-before-{time.time_ns()}', before)
    available = available_backups()
    if not available:
        state.update(phase='rollback_blocked', reason='No healthy 326-grok600 target: '+reason)
        save(state)
        raise RuntimeError('No healthy 326-grok600 target; keeping current route and guard active')
    after = rollback_config(available)
    transition(after, {**state, 'phase': 'rolled_back', 'percent': 0, 'armed': False,
                       'reason': reason, 'rollback_at': time.time()})
    print(json.dumps({'host': HOST, 'rolled_back': True, 'reason': reason}), flush=True)


def check():
    recover_pending()
    state = get_state()
    if not state.get('armed'):
        print(json.dumps({'host': HOST, 'phase': state['phase'], 'armed': False}), flush=True)
        return
    if fingerprint() != state['config_sha256']:
        state.update(phase='disarmed', reason='Configuration changed externally', armed=False)
        save(state)
        raise RuntimeError('Configuration changed; stale guard disarmed')
    try:
        health()
        faults = platform_faults(state)
        if faults:
            raise RuntimeError('Candidate platform panic: '+json.dumps(faults))
        sample = metrics()
        if sample['rss_kb_327'] > 17*1024*1024 or sample['mem_available_kb'] < 8*1024*1024:
            raise RuntimeError('Memory margin exhausted')
        counts = traffic(time.time()-180, started_after=state.get('candidate_at', state['at']))
        # Ingress 5xx includes upstream failures. Without proven platform attribution,
        # observe these counters but never use them to roll back a healthy application.
        probes = counts.get('probe_327', {})
        # Nginx 499 records the caller closing the probe, not a failed health response.
        if any(k not in ('200', '499') and v for k, v in probes.items()):
            raise RuntimeError('Candidate health failed in ingress logs')
    except Exception as exc:
        rollback(type(exc).__name__+': '+str(exc)[:200])
        raise
    state.update(last_check_at=time.time(), successful_checks=state.get('successful_checks', 0)+1)
    save(state)
    sample.update(percent=state['percent'], successful_checks=state['successful_checks'], traffic=counts)
    atomic_text(ROOT/'last-check.json', json.dumps(sample))
    print(json.dumps(sample), flush=True)


def apply(pct, previous):
    if (previous, pct) not in ((0, 1), (1, 100)):
        raise RuntimeError('Unexpected phase transition')
    recover_pending()
    state = get_state()
    if state['percent'] != previous or state['phase'] in ('rolled_back', 'disarmed'):
        raise RuntimeError('Previous phase is not active')
    allowed_before = [config_for(previous).strip()]
    if previous == 0:
        allowed_before.append(rollback_config([ip for ip, _ in SHARES]).strip())
    if fingerprint() != state['config_sha256'] or CONFIG.read_text().strip() not in allowed_before:
        raise RuntimeError('Configuration changed; refusing replacement')
    if previous == 1:
        if time.time()-state['at'] < 180 or state.get('successful_checks', 0) < 12:
            raise RuntimeError('Canary observation is incomplete')
        if time.time()-state.get('last_check_at', 0) > 45:
            raise RuntimeError('Recent watchdog verification missing')
        counts = traffic(state['at'], started_after=state['at'])
        total, _, _ = error_rate(counts.get('business_327', {}))
        stable_total, _, stable_rate = error_rate(counts.get('business_'+STABLE_VERSION, {}))
        successful = sum(v for k, v in counts.get('business_327', {}).items() if k.startswith('2'))
        if total < 50 or successful < 50:
            raise RuntimeError('Insufficient successful business traffic for promotion')
        baseline = stable_rate if stable_total >= 100 else state['baseline_error_rate']
        state.update(baseline_error_rate=baseline, baseline_at=time.time())
    health(include_old=True)
    sample = metrics()
    if sample['mem_available_kb'] < sample['rss_kb_'+STABLE_VERSION]+8*1024*1024:
        raise RuntimeError('Insufficient memory for overlapping versions')
    stage = ROOT/f'phase-{pct}-{time.time_ns()}'
    stage.mkdir(mode=0o700)
    shutil.copy2(CONFIG, stage/'upstream.before')
    atomic_text(stage/'upstream.after', config_for(pct))
    transition(config_for(pct), {**state, 'phase': 'watching', 'percent': pct, 'armed': True, 'at': time.time(),
                                'candidate_at': state.get('candidate_at', time.time()), 'successful_checks': 0})
    check()
    print(json.dumps({'host': HOST, 'percent': pct, 'backup': str(stage)}), flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('action', choices=['init', 'check', 'apply', 'rollback', 'status'])
    parser.add_argument('--percent', type=int)
    parser.add_argument('--previous', type=int)
    args = parser.parse_args()
    if HOST not in CONFIGS:
        raise RuntimeError('Unexpected deployment host')
    ROOT.mkdir(mode=0o700, parents=True, exist_ok=True)
    with open(ROOT/'lock', 'a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            if args.action == 'check':
                print('Guard check skipped: phase change in progress', flush=True)
                return
            raise
        if args.action == 'init':
            assert CONFIG.read_text().strip() in (
                config_for(0).strip(), rollback_config([ip for ip, _ in SHARES]).strip())
            assert not (ROOT/'pids.json').exists()
            pids = {v: int(run('systemctl', 'show', f'sub2api-0.1.{v}-canary', '-p', 'MainPID', '--value')) for v in (STABLE_VERSION, '327')}
            assert all(pids.values())
            atomic_text(ROOT/'pids.json', json.dumps(pids))
            shutil.copy2(CONFIG, ROOT/'upstream.initial')
            health(include_old=True)
            baseline_counts = traffic(time.time()-300)
            _, _, baseline = error_rate(baseline_counts.get('business_'+STABLE_VERSION, {}))
            save({'phase': 'ready', 'percent': 0, 'armed': False, 'at': time.time(), 'baseline_at': time.time(),
                  'rollback_policy': 'platform_health_only',
                  'config_sha256': fingerprint(), 'baseline_error_rate': baseline})
            print(json.dumps({'host': HOST, 'pids': pids, 'baseline': baseline_counts, 'metrics': metrics()}), flush=True)
        elif args.action == 'apply':
            apply(args.percent, args.previous)
        elif args.action == 'check':
            check()
        elif args.action == 'rollback':
            rollback('Operator recovery')
        else:
            print(json.dumps({'state': get_state(), 'latest': json.loads((ROOT/'last-check.json').read_text()) if (ROOT/'last-check.json').exists() else None}), flush=True)


if __name__ == '__main__':
    main()
