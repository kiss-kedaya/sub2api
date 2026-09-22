"""Reuse the rollout engine with 339 retained as the live fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location("rollout340_engine", Path(__file__).with_name("rollout333_guard.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path("/opt/sub2api/rollout-340-20260922")
guard.STABLE_VERSION = "339"
guard.STABLE_PORT = 8120
guard.INITIAL_BACKUP_PORT = 8100
guard.CANDIDATE_VERSION = "340"
guard.CANDIDATE_PORT = 8122

if __name__ == "__main__":
    guard.main()
