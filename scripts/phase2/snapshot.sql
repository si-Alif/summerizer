SELECT now() AS captured_at;

SELECT status, COUNT(*) AS count
FROM sources
GROUP BY status
ORDER BY status;

SELECT current_step, COUNT(*) AS count
FROM sources
GROUP BY current_step
ORDER BY current_step;

SELECT COUNT(*) AS claimable_sources
FROM sources
WHERE status IN ('pending', 'failed')
  AND (next_retry_at IS NULL OR next_retry_at <= now());

SELECT COUNT(*) AS stuck_ingesting_10m
FROM sources
WHERE status = 'ingesting'
  AND updated_at < now() - interval '10 minutes';

SELECT status, COUNT(*) AS count
FROM embedding_jobs
GROUP BY status
ORDER BY status;

SELECT COUNT(*) AS claimable_embedding_jobs
FROM embedding_jobs
WHERE status IN ('pending', 'failed')
  AND attempts < max_attempts
  AND run_after <= now();

SELECT COUNT(*) AS stuck_embedding_processing_10m
FROM embedding_jobs
WHERE status = 'processing'
  AND locked_at IS NOT NULL
  AND locked_at < now() - interval '10 minutes';

SELECT
  COUNT(*) AS total_chunks,
  COUNT(*) FILTER (WHERE embedding IS NOT NULL) AS embedded_chunks,
  COUNT(*) FILTER (WHERE embedding IS NULL) AS unembedded_chunks
FROM chunks;

SELECT id, status, current_step, retry_count, next_retry_at, updated_at, step_error
FROM sources
ORDER BY updated_at DESC
LIMIT 50;

SELECT id, source_id, source_version, status, attempts, max_attempts, run_after, locked_at, locked_by, updated_at, last_error
FROM embedding_jobs
ORDER BY updated_at DESC
LIMIT 50;
