CREATE TABLE IF NOT EXISTS embedding_jobs (
  id BIGSERIAL PRIMARY KEY,
  source_id BIGINT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  source_version INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  run_after TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
  locked_at TIMESTAMP(0) WITH TIME ZONE,
  locked_by TEXT,
  last_error TEXT,
  version INTEGER NOT NULL DEFAULT 1,
  created_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
  updated_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT now(),
  CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'dead')),
  CHECK (attempts >= 0),
  CHECK (max_attempts > 0)
);

CREATE INDEX idx_embedding_jobs_claim
  ON embedding_jobs (status, run_after, id)
  WHERE status IN ('pending', 'failed');

CREATE INDEX idx_embedding_jobs_reclaim
  ON embedding_jobs (status, locked_at)
  WHERE status = 'processing';

CREATE UNIQUE INDEX uq_embedding_jobs_active_source_version
  ON embedding_jobs (source_id, source_version)
  WHERE status IN ('pending', 'processing', 'failed');

CREATE TRIGGER set_embedding_jobs_updated_at
  BEFORE UPDATE ON embedding_jobs
  FOR EACH ROW EXECUTE FUNCTION trigger_set_updated_at();

CREATE OR REPLACE FUNCTION notify_embedding_jobs_queue()
RETURNS TRIGGER AS $$
BEGIN
  PERFORM pg_notify('embedding_jobs', NEW.id::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER embedding_jobs_notify
  AFTER INSERT ON embedding_jobs
  FOR EACH ROW EXECUTE FUNCTION notify_embedding_jobs_queue();