"""Reuse the rollout engine with 343 retained as the live fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location("rollout344_engine", Path(__file__).with_name("rollout333_guard.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path("/opt/sub2api/rollout-344-20260922")
guard.STABLE_VERSION = "343"
guard.STABLE_PORT = 8128
guard.INITIAL_BACKUP_PORT = 8126
guard.CANDIDATE_VERSION = "344"
guard.CANDIDATE_PORT = 8130

if __name__ == "__main__":
    guard.main()
