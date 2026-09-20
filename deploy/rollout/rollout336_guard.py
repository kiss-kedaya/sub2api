"""Reuse the rollout engine with 334 retained as the live fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location('rollout336_engine', Path(__file__).with_name('rollout333_guard.py'))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path('/opt/sub2api/rollout-336-20260920')
guard.STABLE_VERSION = '334'
guard.STABLE_PORT = 8093
guard.INITIAL_BACKUP_PORT = 8107
guard.CANDIDATE_VERSION = '336'
guard.CANDIDATE_PORT = 8096

if __name__ == '__main__':
    guard.main()
