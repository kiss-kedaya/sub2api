-- The application stores heterogeneous historical response_text values.
-- Return a strict allowlist only; malformed legacy text is not a passing audit.
CREATE OR REPLACE FUNCTION channel_operations_audit_summary(raw TEXT)
RETURNS JSONB LANGUAGE plpgsql IMMUTABLE STRICT SECURITY INVOKER
SET search_path = pg_catalog
AS $$
DECLARE
    payload JSONB;
    measured_model TEXT := '';
    decision_status TEXT := '';
BEGIN
    IF octet_length(raw) > 1048576 THEN RETURN NULL; END IF;
    BEGIN
        payload := raw::jsonb;
    EXCEPTION WHEN invalid_text_representation OR untranslatable_character
        OR numeric_value_out_of_range OR program_limit_exceeded THEN
        RETURN NULL;
    END;
    IF jsonb_typeof(payload) IS DISTINCT FROM 'object'
       OR payload->>'provider' IS DISTINCT FROM 'chanshui' THEN
        RETURN NULL;
    END IF;
    IF jsonb_typeof(payload->'model') = 'string' THEN
        measured_model := payload->>'model';
        IF length(measured_model) > 128 OR position('://' IN measured_model) > 0
           OR measured_model !~ '^[A-Za-z0-9_.:/-]+$'
           OR lower(measured_model) ~ '(sk-|api[_-]?key|token|bearer|secret)' THEN
            measured_model := '';
        END IF;
    END IF;
    IF payload->>'audit_status' = 'done'
       AND payload->'decision'->>'status' IN ('audit_pass','audit_fail') THEN
        decision_status := payload->'decision'->>'status';
    END IF;
    RETURN jsonb_build_object('model', measured_model, 'decision_status', decision_status);
END;
$$;
COMMENT ON FUNCTION channel_operations_audit_summary(TEXT) IS
    'Safe Chanshui audit fields only; never exposes probe text, secrets, remote audit IDs or upstream URLs';
