"""Use the verified rollout engine with 333 as the billing-compatible fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location('rollout334_engine', Path(__file__).with_name('rollout333_guard.py'))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path('/opt/sub2api/rollout-334-20260920')
guard.STABLE_VERSION = '333'
guard.STABLE_PORT = 8107
guard.INITIAL_BACKUP_PORT = 8105
guard.CANDIDATE_VERSION = '334'
guard.CANDIDATE_PORT = 8093

if __name__ == '__main__':
    guard.main()
