-- Rolling back the 'deleted' status: the re-added constraint forbids it, so any
-- soft-deleted rows must first be moved to a valid status. Revert them to
-- 'published' (their pre-delete state); deleted_at stays stamped but is no
-- longer enforced. This is lossy — a rollback resurrects soft-deleted documents.
UPDATE documents SET status = 'published' WHERE status = 'deleted';
ALTER TABLE documents DROP CONSTRAINT documents_status_check;
ALTER TABLE documents ADD CONSTRAINT documents_status_check
    CHECK (status IN ('pending', 'published'));
