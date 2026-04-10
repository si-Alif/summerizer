SELECT now() AS captured_at;

SELECT status, COUNT(*) AS count
FROM sources
GROUP BY status
ORDER BY status;

SELECT COUNT(*) AS claimable_sources
FROM sources
WHERE status IN ('pending', 'failed')
  AND (next_retry_at IS NULL OR next_retry_at <= now());

SELECT COUNT(*) AS stuck_ingesting_10m
FROM sources
WHERE status = 'ingesting'
  AND updated_at < now() - interval '10 minutes';

SELECT COALESCE(EXTRACT(EPOCH FROM (now() - MIN(created_at))), 0)::bigint AS oldest_claimable_age_seconds
FROM sources
WHERE status IN ('pending', 'failed')
  AND (next_retry_at IS NULL OR next_retry_at <= now());

SELECT id, status, current_step, retry_count, next_retry_at, updated_at
FROM sources
ORDER BY updated_at DESC
LIMIT 50;
