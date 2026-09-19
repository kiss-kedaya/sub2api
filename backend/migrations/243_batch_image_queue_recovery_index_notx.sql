CREATE INDEX CONCURRENTLY IF NOT EXISTS batch_image_jobs_queue_recovery_idx
    ON batch_image_jobs (id)
    INCLUDE (batch_id)
    WHERE status = 'submitted'
      AND provider_job_name IS NOT NULL
      AND provider_job_name <> ''
      AND last_error_code = 'QUEUE_FAILED';
