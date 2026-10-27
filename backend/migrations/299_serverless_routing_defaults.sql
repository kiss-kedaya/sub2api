-- Online gateway routing is opt-in. Never overwrite an operator's setup.
INSERT INTO settings(key,value,updated_at)
VALUES('serverless_routing_v1','{"revision":"","enabled":false,"established":false,"pods":[],"regions":[]}',NOW())
ON CONFLICT(key) DO NOTHING;
