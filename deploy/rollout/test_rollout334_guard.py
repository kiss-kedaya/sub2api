import datetime
import importlib.util
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('rollout334', Path(__file__).with_name('rollout334_guard.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
guard = module.guard


class Rollout334Tests(unittest.TestCase):
    def test_canary_retains_333_and_full_traffic_keeps_it_as_backup(self):
        canary = guard.config_for(1)
        self.assertIn('10.254.0.2:8107 weight=3960 ', canary)
        self.assertIn('10.254.0.1:8107 weight=5940 ', canary)
        self.assertIn('10.254.0.2:8093 weight=40 ', canary)
        self.assertIn('10.254.0.1:8093 weight=60 ', canary)
        full = guard.config_for(100)
        self.assertIn('10.254.0.2:8107 weight=40 max_fails=3 fail_timeout=10s backup;', full)
        self.assertNotIn(':8105', guard.rollback_config([ip for ip, _ in guard.SHARES]))

    def test_traffic_is_attributed_to_final_candidate_port(self):
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory)/'access.log'
            row = {'ts': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'request_time': '0.5',
                   'upstream': '10.254.0.2:8107, 10.254.0.1:8093', 'status': 200, 'probe': False}
            log.write_text(json.dumps(row)+'\n')
            with patch.object(guard, 'Path', return_value=log):
                counts = guard.traffic(time.time()-60)
            self.assertEqual(counts, {'business_334': {'200': 1}})

    def test_failed_health_rolls_back_to_333_without_stopping_processes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(guard, 'ROOT', root), patch.object(guard, 'CONFIG', root/'upstream.conf'):
                guard.CONFIG.write_text(guard.config_for(100))
                guard.save({'phase': 'watching', 'percent': 100, 'armed': True, 'at': time.time()-600,
                            'baseline_error_rate': 0, 'config_sha256': guard.fingerprint()})
                with patch.object(guard, 'health', side_effect=RuntimeError('probe failed')), \
                        patch.object(guard, 'available_backups', return_value=[ip for ip, _ in guard.SHARES]), \
                        patch.object(guard, 'run', return_value='') as run:
                    with self.assertRaises(RuntimeError):
                        guard.check()
                self.assertEqual(guard.get_state()['phase'], 'rolled_back')
                self.assertIn(':8107 ', guard.CONFIG.read_text())
                self.assertNotIn(':8093 ', guard.CONFIG.read_text())
                self.assertEqual([call.args for call in run.call_args_list],
                                 [('nginx', '-t'), ('systemctl', 'reload', 'nginx')])


if __name__ == '__main__':
    unittest.main()
