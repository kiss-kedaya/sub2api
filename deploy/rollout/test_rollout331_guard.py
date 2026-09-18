import importlib.util
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('guard', Path(__file__).with_name('rollout331_guard.py'))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class RolloutTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.calls = []
        for name, value in [('ROOT', self.root), ('CONFIG', self.root/'upstream.conf'), ('NAME', 'test'), ('HOST', 'ns575199'), ('STABLE_VERSION', '330'), ('STABLE_PORT', 8103), ('INITIAL_BACKUP_PORT', 8101), ('UI_ENTRY', self.root/'ui'/'index.html')]:
            p = patch.object(guard, name, value)
            p.start()
            self.addCleanup(p.stop)
        guard.CONFIG.write_text(guard.config_for(100))
        guard.save({'phase': 'watching', 'percent': 100, 'armed': True, 'at': time.time()-200,
                    'baseline_error_rate': 0.03, 'config_sha256': guard.fingerprint()})
        p = patch.object(guard, 'run', self.mock_run)
        p.start()
        self.addCleanup(p.stop)
        p = patch.object(guard, 'available_backups', return_value=[ip for ip, _ in guard.SHARES])
        p.start()
        self.addCleanup(p.stop)
        p = patch.object(guard, 'platform_faults', return_value={})
        p.start()
        self.addCleanup(p.stop)

    def mock_run(self, *args):
        self.calls.append(args)
        if args not in [('nginx', '-t'), ('systemctl', 'reload', 'nginx')]:
            raise AssertionError('Unexpected production operation: '+str(args))
        return ''

    def test_health_failure_restores_stable_without_stopping_any_process(self):
        with patch.object(guard, 'health', side_effect=RuntimeError('probe failed')):
            with self.assertRaises(RuntimeError):
                guard.check()
        self.assertEqual(guard.CONFIG.read_text(), guard.rollback_config([ip for ip, _ in guard.SHARES]))
        self.assertNotIn(':8105', guard.CONFIG.read_text())
        state = guard.get_state()
        self.assertEqual(state['phase'], 'rolled_back')
        self.assertFalse(state['armed'])
        self.assertEqual(self.calls, [('nginx', '-t'), ('systemctl', 'reload', 'nginx')])

    def test_stale_guard_never_overwrites_newer_deployment(self):
        guard.CONFIG.write_text('newer deployment\n')
        with self.assertRaises(RuntimeError):
            guard.check()
        self.assertEqual(guard.CONFIG.read_text(), 'newer deployment\n')
        self.assertEqual(guard.get_state()['phase'], 'disarmed')
        self.assertEqual(self.calls, [])

    def test_config_validation_failure_restores_on_disk_config(self):
        original = guard.CONFIG.read_text()
        with patch.object(guard, 'run', side_effect=RuntimeError('invalid config')):
            with self.assertRaises(RuntimeError):
                guard.rollback('test')
        self.assertEqual(guard.CONFIG.read_text(), original)
        self.assertTrue(guard.get_state()['armed'])

    def test_unattributed_upstream_errors_do_not_roll_back(self):
        sample = {'rss_kb_331': 1024, 'mem_available_kb': 32*1024*1024}
        counts = {'business_331': {'200': 35, '503': 15}, 'business_330': {'200': 98, '503': 2}}
        with patch.object(guard, 'health'), patch.object(guard, 'metrics', return_value=sample), patch.object(guard, 'traffic', return_value=counts):
            guard.check()
        self.assertTrue(guard.get_state()['armed'])
        self.assertEqual(self.calls, [])

    def test_existing_upstream_errors_do_not_trigger_version_regression(self):
        sample = {'rss_kb_331': 1024, 'mem_available_kb': 32*1024*1024}
        counts = {'business_331': {'200': 97, '503': 3}, 'business_330': {'200': 97, '503': 3}}
        with patch.object(guard, 'health'), patch.object(guard, 'metrics', return_value=sample), patch.object(guard, 'traffic', return_value=counts):
            guard.check()
        self.assertTrue(guard.get_state()['armed'])
        self.assertEqual(guard.get_state()['successful_checks'], 1)
        self.assertEqual(self.calls, [])

    def test_percentages_and_backups(self):
        canary = guard.config_for(1)
        self.assertIn('10.254.0.2:8103 weight=3960 ', canary)
        self.assertIn('10.254.0.1:8103 weight=5940 ', canary)
        self.assertIn('10.254.0.2:8105 weight=40 ', canary)
        self.assertIn('10.254.0.1:8105 weight=60 ', canary)
        full = guard.config_for(100)
        self.assertIn('10.254.0.2:8105 weight=4000 ', full)
        self.assertIn('10.254.0.1:8105 weight=6000 ', full)
        self.assertEqual(full.count('backup;'), 2)
        self.assertNotIn(':8101', full)

    def test_initial_configs_match_330_full_routes(self):
        old_expected = '''upstream test {
    server 10.254.0.2:8103 weight=4000 max_fails=3 fail_timeout=10s;
    server 10.254.0.1:8103 weight=6000 max_fails=3 fail_timeout=10s;
    server 10.254.0.2:8101 weight=40 max_fails=3 fail_timeout=10s backup;
    server 10.254.0.1:8101 weight=60 max_fails=3 fail_timeout=10s backup;
}
'''
        self.assertEqual(guard.config_for(0), old_expected)
        with patch.object(guard, 'HOST', 'ns572368'):
            self.assertEqual(guard.config_for(0), old_expected)

    def test_platform_panic_rolls_back_without_stopping_applications(self):
        with patch.object(guard, 'health'), patch.object(guard, 'platform_faults', return_value={'openai.responses_panic_recovered': 1}):
            with self.assertRaisesRegex(RuntimeError, 'platform panic'):
                guard.check()
        self.assertEqual(guard.get_state()['phase'], 'rolled_back')
        self.assertEqual(self.calls, [('nginx', '-t'), ('systemctl', 'reload', 'nginx')])

    def test_upstream_payload_cannot_forge_platform_panic(self):
        rows = [json.dumps({'msg': 'openai.forward_failed', 'error': 'panic: upstream crash'}),
                json.dumps({'msg': 'upstream_http_error', 'response': {'msg': 'openai.responses_panic_recovered'}})]
        self.assertEqual(guard.find_platform_faults('\n'.join(rows)), {})
        self.assertEqual(guard.find_platform_faults(json.dumps({'msg': 'openai.responses_panic_recovered'})), {'openai.responses_panic_recovered': 1})

    def test_new_ingress_preserves_its_329_backup(self):
        with patch.object(guard, 'HOST', 'ns572368'):
            baseline = guard.config_for(0)
            self.assertIn('10.254.0.2:8103 weight=4000 ', baseline)
            self.assertIn('10.254.0.1:8101 weight=60 max_fails=3 fail_timeout=10s backup;', baseline)
            canary = guard.config_for(1)
            self.assertIn('10.254.0.1:8103 weight=5940 ', canary)
            self.assertIn('10.254.0.2:8105 weight=40 ', canary)
            full = guard.config_for(100)
            self.assertIn('10.254.0.1:8103 weight=60 max_fails=3 fail_timeout=10s backup;', full)
            rollback = guard.rollback_config(['10.254.0.1'])
            self.assertIn('10.254.0.1:8103', rollback)
            self.assertNotIn('10.254.0.2:', rollback)
            self.assertNotIn(':8105', rollback)

    def test_interrupted_reload_is_recovered_on_next_tick(self):
        with patch.object(guard, 'run', side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                guard.rollback('interrupted')
        self.assertIn('pending', guard.get_state())
        self.assertEqual(guard.CONFIG.read_text(), guard.rollback_config([ip for ip, _ in guard.SHARES]))
        guard.check()
        self.assertEqual(guard.get_state()['phase'], 'rolled_back')
        self.assertNotIn('pending', guard.get_state())
        self.assertEqual(self.calls, [('nginx', '-t'), ('systemctl', 'reload', 'nginx')])

    def test_no_healthy_backup_keeps_route_and_guard(self):
        before = guard.CONFIG.read_text()
        with patch.object(guard, 'available_backups', return_value=[]):
            with self.assertRaisesRegex(RuntimeError, 'No healthy 330'):
                guard.rollback('candidate failed')
        self.assertEqual(guard.CONFIG.read_text(), before)
        self.assertTrue(guard.get_state()['armed'])
        self.assertEqual(guard.get_state()['phase'], 'rollback_blocked')
        self.assertEqual(self.calls, [])

    def test_small_failed_sample_blocks_full_promotion(self):
        guard.CONFIG.write_text(guard.config_for(1))
        state = guard.get_state()
        state.update(percent=1, successful_checks=15, last_check_at=time.time(), config_sha256=guard.fingerprint())
        guard.save(state)
        with patch.object(guard, 'traffic', return_value={'business_331': {'503': 49}}):
            with self.assertRaisesRegex(RuntimeError, 'Insufficient successful business'):
                guard.apply(100, 1)
        self.assertEqual(guard.get_state()['percent'], 1)
        self.assertEqual(self.calls, [])

    def test_slow_recently_finished_error_is_counted(self):
        stamp = time.time()
        log = self.root/'slow.log'
        log.write_text(json.dumps({'ts': guard.datetime.datetime.fromtimestamp(stamp, guard.datetime.timezone.utc).isoformat(),
                                   'request_time': '240', 'upstream': '10.254.0.1:8105', 'status': '504', 'probe': False})+'\n')
        with patch.object(guard, 'Path', side_effect=lambda value: log if 'sub2api-rollout.log' in str(value) else Path(value)):
            counts = guard.traffic(stamp-180, started_after=stamp-600)
        self.assertEqual(counts, {'business_331': {'504': 1}})

    def test_stale_baseline_does_not_claim_version_regression(self):
        state = guard.get_state()
        state['baseline_at'] = time.time()-1800
        guard.save(state)
        sample = {'rss_kb_331': 1024, 'mem_available_kb': 32*1024*1024}
        with patch.object(guard, 'health'), patch.object(guard, 'metrics', return_value=sample), patch.object(guard, 'traffic', return_value={'business_331': {'200': 80, '503': 20}}):
            guard.check()
        self.assertTrue(guard.get_state()['armed'])
        self.assertEqual(self.calls, [])

    def test_pending_rollback_rechecks_target_and_restores_previous_route(self):
        original = guard.CONFIG.read_text()
        with patch.object(guard, 'run', side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                guard.rollback('interrupted')
        with patch.object(guard, 'available_backups', return_value=[]):
            with self.assertRaisesRegex(RuntimeError, 'No healthy 330 target during recovery'):
                guard.recover_pending()
        self.assertEqual(guard.CONFIG.read_text(), original)
        self.assertTrue(guard.get_state()['armed'])
        self.assertIn('pending', guard.get_state())
        with patch.object(guard, 'available_backups', return_value=['10.254.0.2']):
            guard.recover_pending()
        self.assertEqual(guard.get_state()['phase'], 'rolled_back')
        self.assertNotIn('10.254.0.1:8103', guard.CONFIG.read_text())

    def test_recovered_canary_starts_observation_after_successful_reload(self):
        guard.CONFIG.write_text(guard.config_for(0))
        state = guard.get_state()
        state.update(percent=0, config_sha256=guard.fingerprint())
        guard.save(state)
        with patch.object(guard, 'run', side_effect=KeyboardInterrupt):
            with self.assertRaises(KeyboardInterrupt):
                guard.transition(guard.config_for(1), {**state, 'phase': 'watching', 'percent': 1, 'at': 1})
        with patch.object(guard.time, 'time', return_value=5000):
            guard.recover_pending()
        state = guard.get_state()
        self.assertEqual(state['at'], 5000)
        self.assertEqual(state['candidate_at'], 5000)
        self.assertEqual(state['successful_checks'], 0)

    def test_single_healthy_original_is_the_only_rollback_target(self):
        with patch.object(guard, 'available_backups', return_value=['10.254.0.2']):
            guard.rollback('candidate failed')
        config = guard.CONFIG.read_text()
        self.assertIn('10.254.0.2:8103', config)
        self.assertNotIn('10.254.0.1:', config)
        self.assertNotIn(':8095', config)
        self.assertNotIn(':8105', config)

    def test_one_success_does_not_allow_promotion(self):
        guard.CONFIG.write_text(guard.config_for(1))
        state = guard.get_state()
        state.update(percent=1, successful_checks=15, last_check_at=time.time(), config_sha256=guard.fingerprint())
        guard.save(state)
        with patch.object(guard, 'traffic', return_value={'business_331': {'200': 1, '503': 49}}):
            with self.assertRaisesRegex(RuntimeError, 'Insufficient successful business'):
                guard.apply(100, 1)
        self.assertEqual(guard.get_state()['percent'], 1)
        self.assertEqual(self.calls, [])

    def test_retry_is_attributed_to_final_8105_port(self):
        stamp = time.time()
        log = self.root/'retry.log'
        log.write_text(json.dumps({'ts': guard.datetime.datetime.fromtimestamp(stamp, guard.datetime.timezone.utc).isoformat(),
                                   'request_time': '1', 'upstream': '10.254.0.1:8103, 10.254.0.2:8105',
                                   'status': '200', 'probe': False})+'\n')
        with patch.object(guard, 'Path', side_effect=lambda value: log if 'sub2api-rollout.log' in str(value) else Path(value)):
            self.assertEqual(guard.traffic(stamp-180), {'business_331': {'200': 1}})

    def test_client_cancelled_health_probe_does_not_roll_back(self):
        sample = {'rss_kb_331': 1024, 'mem_available_kb': 32*1024*1024}
        counts = {'probe_331': {'200': 1264, '499': 1}}
        with patch.object(guard, 'health'), patch.object(guard, 'metrics', return_value=sample), patch.object(guard, 'traffic', return_value=counts):
            guard.check()
        self.assertTrue(guard.get_state()['armed'])
        self.assertEqual(self.calls, [])

    def test_health_server_error_still_rolls_back(self):
        sample = {'rss_kb_331': 1024, 'mem_available_kb': 32*1024*1024}
        with patch.object(guard, 'health'), patch.object(guard, 'metrics', return_value=sample), patch.object(guard, 'traffic', return_value={'probe_331': {'200': 1264, '503': 1}}):
            with self.assertRaisesRegex(RuntimeError, 'Candidate health failed'):
                guard.check()
        self.assertEqual(guard.get_state()['phase'], 'rolled_back')

    def health_run(self, public_failures=0, origin_failure=False):
        (self.root/'pids.json').write_text(json.dumps({'330': 3300, '331': 3310}))
        calls = []
        def run(*args):
            calls.append(args)
            if args[0] == 'curl':
                url = args[-1]
                if origin_failure and url == 'http://10.254.0.1:8105/readyz':
                    raise RuntimeError('origin unavailable')
                if url == 'https://kedaya.ai/readyz' and '--resolve' not in args:
                    count = sum(call[-1] == url and '--resolve' not in call for call in calls)
                    if count <= public_failures:
                        raise guard.subprocess.CalledProcessError(28, args, 'Timeout')
                return json.dumps({'status': 'ok' if url.endswith('/health') else 'ready'})
            if args[:2] == ('systemctl', 'show'):
                return '3310' if '331' in args[2] else '3300'
            if args[:2] == ('systemctl', 'is-active'):
                return 'active'
            raise AssertionError(args)
        return run, calls

    def test_one_public_timeout_is_confirmed_again_with_healthy_origin(self):
        run, calls = self.health_run(public_failures=1)
        with patch.object(guard, 'run', run):
            guard.health()
        public = [args for args in calls if args[-1] == 'https://kedaya.ai/readyz' and '--resolve' not in args]
        self.assertEqual(len(public), 2)

    def test_repeated_public_timeout_remains_a_failure(self):
        run, _ = self.health_run(public_failures=2)
        with patch.object(guard, 'run', run):
            with self.assertRaises(guard.subprocess.CalledProcessError):
                guard.health()

    def test_origin_failure_is_not_hidden_by_public_success(self):
        run, _ = self.health_run(origin_failure=True)
        with patch.object(guard, 'run', run):
            with self.assertRaisesRegex(RuntimeError, 'origin unavailable'):
                guard.health()

    def test_public_http_failure_is_not_retried(self):
        run, _ = self.health_run()
        def http_failure(*args):
            if args[-1] == 'https://kedaya.ai/readyz' and '--resolve' not in args:
                raise guard.subprocess.CalledProcessError(22, args, '503')
            return run(*args)
        with patch.object(guard, 'run', http_failure):
            with self.assertRaises(guard.subprocess.CalledProcessError) as result:
                guard.health()
        self.assertEqual(result.exception.returncode, 22)
        self.assertFalse((self.root/'last-public-timeout.json').exists())

    def test_public_timeout_with_origin_failure_stops_before_public_retry(self):
        run, calls = self.health_run(public_failures=1)
        def origin_fails_after_timeout(*args):
            public_seen = any(call[-1] == 'https://kedaya.ai/readyz' and '--resolve' not in call for call in calls)
            if public_seen and args[-1] == 'http://10.254.0.1:8105/readyz':
                raise RuntimeError('origin failed during verification')
            return run(*args)
        with patch.object(guard, 'run', origin_fails_after_timeout):
            with self.assertRaisesRegex(RuntimeError, 'origin failed during verification'):
                guard.health()
        self.assertEqual(sum(call[-1] == 'https://kedaya.ai/readyz' and '--resolve' not in call for call in calls), 1)

    def test_resume_from_rollback_route_preserves_previous_phase_files(self):
        guard.CONFIG.write_text(guard.rollback_config([ip for ip, _ in guard.SHARES]))
        state = guard.get_state()
        state.update(phase='ready', percent=0, armed=False, config_sha256=guard.fingerprint())
        guard.save(state)
        previous = self.root/'phase-1'
        previous.mkdir()
        (previous/'upstream.before').write_text('original evidence')
        sample = {'rss_kb_330': 1024, 'rss_kb_331': 1024, 'mem_available_kb': 32*1024*1024}
        with patch.object(guard, 'health'), patch.object(guard, 'metrics', return_value=sample), patch.object(guard, 'traffic', return_value={}):
            guard.apply(1, 0)
        self.assertEqual(guard.CONFIG.read_text(), guard.config_for(1))
        self.assertEqual(guard.get_state()['percent'], 1)
        self.assertEqual((previous/'upstream.before').read_text(), 'original evidence')

    def install_ui_transaction(self, current):
        guard.UI_ENTRY.parent.mkdir()
        guard.UI_ENTRY.write_text(current)
        before = self.root/'ui-config-backup'/'index.html'
        before.parent.mkdir()
        before.write_text('stable ui')
        (self.root/'ui-active.json').write_text(json.dumps({
            'path': str(guard.UI_ENTRY),
            'before': str(before),
            'after_sha256': guard.hashlib.sha256(b'candidate ui').hexdigest(),
        }))

    def test_rollback_restores_owned_ui_atomically(self):
        self.install_ui_transaction('candidate ui')
        guard.rollback('candidate failed')
        self.assertEqual(guard.UI_ENTRY.read_text(), 'stable ui')
        self.assertEqual(json.loads((self.root/'ui-rollback.json').read_text())['status'], 'restored')
        self.assertFalse((guard.UI_ENTRY.parent/'index.html.331-tmp').exists())

    def test_rollback_accepts_ui_already_at_before(self):
        self.install_ui_transaction('stable ui')
        guard.rollback('candidate failed')
        self.assertEqual(guard.UI_ENTRY.read_text(), 'stable ui')
        self.assertEqual(json.loads((self.root/'ui-rollback.json').read_text())['status'], 'already_before')

    def test_rollback_preserves_externally_modified_ui(self):
        self.install_ui_transaction('external ui')
        guard.rollback('candidate failed')
        self.assertEqual(guard.UI_ENTRY.read_text(), 'external ui')
        self.assertEqual(json.loads((self.root/'ui-rollback.json').read_text())['status'],
                         'external_change_preserved')

    def test_ui_health_checks_every_local_script_and_stylesheet(self):
        guard.UI_ENTRY.parent.mkdir()
        guard.UI_ENTRY.write_text('<link rel="stylesheet" href="/assets/app.css"><script src="assets/app.js"></script>')
        assets = guard.UI_ENTRY.parent/'assets'
        assets.mkdir()
        (assets/'app.css').write_text('body{}')
        (assets/'app.js').write_text('ok')
        guard.ui_health()
        (assets/'app.js').unlink()
        with self.assertRaisesRegex(RuntimeError, 'UI asset unavailable'):
            guard.ui_health()


if __name__ == '__main__':
    unittest.main(verbosity=2)
