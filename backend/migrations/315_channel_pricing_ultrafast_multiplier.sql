-- Per-channel Ultrafast (service_tier=ultrafast) billing multiplier.
-- NULL keeps the model-owned default (currently 6x for Opus 5.5 and the
-- catalog ratio for everything else), so existing rows are unaffected.
ALTER TABLE channel_model_pricing
  ADD COLUMN IF NOT EXISTS ultrafast_multiplier numeric(12, 4);
