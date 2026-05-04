CREATE OR REPLACE FUNCTION notify_sources_queue()
RETURNS TRIGGER AS $$
BEGIN
  PERFORM pg_notify('sources' , NEW.id::text);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER sources_notify
  AFTER INSERT ON sources
  FOR EACH ROW EXECUTE FUNCTION notify_sources_queue();