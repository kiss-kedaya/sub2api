import hashlib
import json
from pathlib import Path
import runpy
import sys
import tempfile
import types
import unittest
from unittest.mock import patch


class ResumeTests(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.source = self.root/'candidate.py'
        self.script = b'from resume_fixture import *\n'
        self.source.write_bytes(self.script)
        self.config = self.root/'upstream.conf'
        self.config.write_text('original 321 route')
        self.before = {'phase': 'rolled_back', 'percent': 0, 'armed': False,
                       'reason': 'old reason', 'config_sha256': self.fingerprint()}
        self.save(self.before)
        for name in ('rollout322_guard.py', 'pids.json', 'last-check.json'):
            (self.root/name).write_text('original evidence')
        self.fixture = types.ModuleType('resume_fixture')
        self.fixture.HOST = 'test'
        self.fixture.CONFIGS = {'test': 'test'}
        self.fixture.ROOT = self.root
        self.fixture.CONFIG = self.config
        self.fixture.SHARES = [('test', 100)]
        self.fixture.get_state = self.get_state
        self.fixture.save = self.save
        self.fixture.fingerprint = self.fingerprint
        self.fixture.rollback_config = lambda ips: 'original 321 route'
        self.fixture.health = lambda **kwargs: None
        self.fixture.metrics = lambda: {'mem_available_kb': 32*1024*1024, 'rss_kb_321': 1024}
        self.fixture.run = lambda *args: 'active'
        self.fixture.traffic = lambda cutoff: {}
        self.fixture.error_rate = lambda rows: (0, 0, 0)
        self.fixture.apply = lambda pct, previous: self.save({**self.get_state(), 'phase': 'watching', 'percent': pct, 'armed': True})

    def fingerprint(self):
        return hashlib.sha256(self.config.read_bytes()).hexdigest()

    def save(self, state):
        (self.root/'state.json').write_text(json.dumps(state))

    def get_state(self):
        return json.loads((self.root/'state.json').read_text())

    def resume(self):
        real_path = Path
        def mapped(value):
            return self.source if str(value) == '/tmp/rollout322_guard.py' else real_path(value)
        digest = hashlib.sha256(self.script).hexdigest()
        with patch.dict(sys.modules, {'resume_fixture': self.fixture}), patch.object(sys, 'argv', ['resume', digest]), patch('pathlib.Path', mapped):
            runpy.run_path(str(Path(__file__).with_name('resume322.py')))

    def test_external_config_change_during_preflight_is_never_adopted(self):
        self.fixture.health = lambda **kwargs: self.config.write_text('external route')
        with self.assertRaisesRegex(AssertionError, 'Configuration changed'):
            self.resume()
        self.assertEqual(self.config.read_text(), 'external route')
        self.assertEqual(self.get_state(), self.before)

    def test_new_rollback_reason_is_preserved(self):
        def apply(pct, previous):
            self.save({**self.get_state(), 'phase': 'rolled_back', 'percent': 0, 'armed': False,
                       'reason': 'new candidate failure', 'rollback_at': 123})
            raise RuntimeError('new candidate failure')
        self.fixture.apply = apply
        with self.assertRaisesRegex(RuntimeError, 'new candidate failure'):
            self.resume()
        self.assertEqual(self.get_state()['reason'], 'new candidate failure')
        self.assertEqual(self.get_state()['rollback_at'], 123)

    def test_source_change_after_validation_does_not_change_installed_bytes(self):
        self.fixture.health = lambda **kwargs: self.source.write_text('raise RuntimeError("replaced")')
        self.resume()
        self.assertEqual((self.root/'rollout322_guard.py').read_bytes(), self.script)

    def test_pretransition_failure_restores_original_state(self):
        def apply(pct, previous):
            raise RuntimeError('pretransition failed')
        self.fixture.apply = apply
        with self.assertRaisesRegex(RuntimeError, 'pretransition failed'):
            self.resume()
        self.assertEqual(self.get_state(), self.before)

    def test_pending_transaction_is_preserved_for_guard(self):
        def apply(pct, previous):
            self.save({**self.get_state(), 'pending': {'test': True}})
            raise RuntimeError('reload interrupted')
        self.fixture.apply = apply
        with self.assertRaisesRegex(RuntimeError, 'reload interrupted'):
            self.resume()
        self.assertIn('pending', self.get_state())


if __name__ == '__main__':
    unittest.main(verbosity=2)
