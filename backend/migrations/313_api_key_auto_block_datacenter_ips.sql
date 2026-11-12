-- 313_api_key_auto_block_datacenter_ips.sql
-- Per-key opt-in for the periodic datacenter/proxy IP blacklist job. When the
-- flag is on, the background runner appends the hosting/proxy IPs a key was
-- recently used from to that key's ip_blacklist (append-only; a failed lookup
-- adds nothing). Off by default so existing keys keep their current behavior.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS auto_block_datacenter_ips BOOLEAN NOT NULL DEFAULT FALSE;
