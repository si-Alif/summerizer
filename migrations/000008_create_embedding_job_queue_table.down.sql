DROP TRIGGER IF EXISTS embedding_jobs_notify ON embedding_jobs;
DROP FUNCTION IF EXISTS notify_embedding_jobs_queue();
DROP TRIGGER IF EXISTS set_embedding_jobs_updated_at ON embedding_jobs;
DROP INDEX IF EXISTS uq_embedding_jobs_active_source_version;
DROP INDEX IF EXISTS idx_embedding_jobs_reclaim;
DROP INDEX IF EXISTS idx_embedding_jobs_claim;
DROP TABLE IF EXISTS embedding_jobs;