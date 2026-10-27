-- Gateway restrictions are separate from account login status.
CREATE TABLE IF NOT EXISTS security_penalty_subjects (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 scope TEXT NOT NULL CHECK (scope IN ('user','session')),
 subject_key TEXT NOT NULL,
 policy TEXT NOT NULL CHECK (policy IN ('session_block','user_block','session_route_then_block')),
 strike_count INTEGER NOT NULL DEFAULT 0 CHECK (strike_count >= 0),
 window_started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 blocked_until TIMESTAMPTZ,
 route_until TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(user_id,scope,subject_key,policy)
);
CREATE TABLE IF NOT EXISTS security_penalty_events (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 api_key_id BIGINT,
 group_id BIGINT,
 request_id TEXT NOT NULL,
 session_key TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL CHECK (source IN ('content_moderation','prompt_audit')),
 scope TEXT NOT NULL CHECK (scope IN ('user','session','request')),
 policy TEXT NOT NULL,
 reason TEXT NOT NULL,
 full_prompt TEXT NOT NULL,
 model TEXT NOT NULL DEFAULT '',
 endpoint TEXT NOT NULL DEFAULT '',
 strike_count INTEGER NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(user_id,request_id)
);
CREATE INDEX IF NOT EXISTS idx_security_penalty_events_owner ON security_penalty_events(user_id,id DESC);
CREATE TABLE IF NOT EXISTS security_penalty_releases (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
 reason TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS security_penalty_request_context (
 user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 request_id VARCHAR(256) NOT NULL,
 session_key VARCHAR(64) NOT NULL DEFAULT '',
 expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(user_id,request_id)
);
CREATE INDEX IF NOT EXISTS idx_security_penalty_request_expiry ON security_penalty_request_context(expires_at);
CREATE INDEX IF NOT EXISTS idx_security_penalty_events_created ON security_penalty_events(created_at,id);
INSERT INTO settings(key,value) VALUES ('security_penalty_config','{"enabled":false,"observe_only":true,"policy":"session_block","threshold":3,"window_hours":720,"duration_hours":24,"all_groups":true,"group_ids":[],"exempt_user_ids":[],"version":1}') ON CONFLICT(key) DO NOTHING;
