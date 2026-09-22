"""Reuse the rollout engine with 342 retained as the live fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location("rollout343_engine", Path(__file__).with_name("rollout333_guard.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path("/opt/sub2api/rollout-343-20260922")
guard.STABLE_VERSION = "342"
guard.STABLE_PORT = 8126
guard.INITIAL_BACKUP_PORT = 8124
guard.CANDIDATE_VERSION = "343"
guard.CANDIDATE_PORT = 8128

if __name__ == "__main__":
    guard.main()
