from pathlib import Path
import re


CONFIG = Path(__file__).with_name("sub2api-backend-locations.conf").read_text(encoding="utf-8")
match = re.search(r"(?m)^location = /chat/completions\s*\{([^}]*)\}", CONFIG)
assert match, "bare chat completions path must have an explicit location"
block = match.group(1)
assert "return 400" in block, "bare endpoint should guide, not proxy or login-redirect"
assert "application/json" in block
assert "/v1/chat/completions" in block, "show the corrected path to API clients"
assert "proxy_pass" not in block
