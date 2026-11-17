-- DeepSeek OCR price per 1,000 successfully recognized pages, in the same unit
-- as the other prices of the group. NULL = built-in default (official list
-- price); explicit 0 = free; positive = group override.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS ocr_price_per_1k_pages DECIMAL(20,8);
COMMENT ON COLUMN groups.ocr_price_per_1k_pages IS 'OCR price per 1,000 pages; NULL = default, 0 = free, positive = override';
