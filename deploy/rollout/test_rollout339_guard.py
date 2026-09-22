import importlib.util
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("rollout339", Path(__file__).with_name("rollout339_guard.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
guard = module.guard


class Rollout339Tests(unittest.TestCase):
    def test_canary_and_full_traffic_keep_338_available(self):
        canary = guard.config_for(1)
        self.assertIn("10.254.0.2:8100 weight=3960 ", canary)
        self.assertIn("10.254.0.1:8100 weight=5940 ", canary)
        self.assertIn("10.254.0.2:8120 weight=40 ", canary)
        self.assertIn("10.254.0.1:8120 weight=60 ", canary)
        self.assertIn("10.254.0.2:8096 weight=40 max_fails=3 fail_timeout=10s backup;", canary)
        full = guard.config_for(100)
        self.assertIn("10.254.0.2:8120 weight=4000 ", full)
        self.assertIn("10.254.0.1:8120 weight=6000 ", full)
        self.assertIn("10.254.0.2:8100 weight=40 max_fails=3 fail_timeout=10s backup;", full)
        self.assertNotIn(":8096", guard.rollback_config([ip for ip, _ in guard.SHARES]))
        self.assertNotIn(":8120", guard.rollback_config([ip for ip, _ in guard.SHARES]))

    def test_failed_health_restores_338_and_ui_without_stopping_processes(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(guard, "ROOT", root), patch.object(guard, "CONFIG", root/"upstream.conf"):
                guard.CONFIG.write_text(guard.config_for(100))
                guard.save({"phase": "watching", "percent": 100, "armed": True, "at": time.time()-600,
                            "baseline_error_rate": 0, "config_sha256": guard.fingerprint()})
                with patch.object(guard, "health", side_effect=RuntimeError("probe failed")),                         patch.object(guard, "available_backups", return_value=[ip for ip, _ in guard.SHARES]),                         patch.object(guard, "restore_ui") as restore_ui,                         patch.object(guard, "run", return_value="") as run:
                    with self.assertRaises(RuntimeError):
                        guard.check()
                self.assertEqual(guard.get_state()["phase"], "rolled_back")
                self.assertIn(":8100 ", guard.CONFIG.read_text())
                self.assertNotIn(":8120 ", guard.CONFIG.read_text())
                restore_ui.assert_called_once()
                self.assertEqual([call.args for call in run.call_args_list],
                                 [("nginx", "-t"), ("systemctl", "reload", "nginx")])


if __name__ == "__main__":
    unittest.main()
