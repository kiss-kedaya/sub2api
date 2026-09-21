"""Reuse the rollout engine with 336 retained as the live fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location("rollout338_engine", Path(__file__).with_name("rollout333_guard.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path("/opt/sub2api/rollout-338-20260921")
guard.STABLE_VERSION = "336"
guard.STABLE_PORT = 8096
guard.INITIAL_BACKUP_PORT = 8098
guard.CANDIDATE_VERSION = "338"
guard.CANDIDATE_PORT = 8100

if __name__ == "__main__":
    guard.main()
