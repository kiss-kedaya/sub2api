"""Reuse the rollout engine with 340 retained as the live fallback."""

import importlib.util
from pathlib import Path

spec = importlib.util.spec_from_file_location("rollout341_engine", Path(__file__).with_name("rollout333_guard.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)
guard.ROOT = Path("/opt/sub2api/rollout-341-20260922")
guard.STABLE_VERSION = "340"
guard.STABLE_PORT = 8122
guard.INITIAL_BACKUP_PORT = 8120
guard.CANDIDATE_VERSION = "341"
guard.CANDIDATE_PORT = 8124

if __name__ == "__main__":
    guard.main()
