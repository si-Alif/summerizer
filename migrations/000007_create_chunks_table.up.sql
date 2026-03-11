CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS chunks(
  id bigserial PRIMARY KEY ,
  source_id bigint NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
  chunk_index integer NOT NULL,
  content text NOT NULL,
  token_count integer NOT NULL,
  --- 768 dimensions as nomic-embed-text would be used ---
  embedding vector(768),
  metadata jsonb NOT NULL DEFAULT '{}',
  created_at timestamp(0) with time zone NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_chunks_source_chunk ON chunks(source_id , chunk_index);

CREATE INDEX idx_chunks_embedding_hnsw ON chunks USING hnsw(embedding vector_cosine_ops) WITH (m=16 , ef_construction=64);