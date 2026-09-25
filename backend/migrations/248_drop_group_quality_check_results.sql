-- 248: Drop the standalone group quality check probe table.
--
-- Group degradation (降智) status is now derived from the existing scheduled
-- test results (scheduled_test_results), which the per-account scheduled test
-- plans already produce. Running a second, independent probe loop duplicated
-- that work and is removed; the settings table stays as the per-group toggle.
DROP TABLE IF EXISTS group_quality_check_results;
