-- A terminal operation retains its idempotency binding for a configured
-- response-retry window. After expiry the durable operation remains auditable,
-- but replaying the same key returns operation_expired instead of silently
-- creating a new command.
ALTER TABLE operations ADD COLUMN idempotency_expires_at INTEGER;

CREATE INDEX operations_idempotency_expiry_idx
    ON operations(idempotency_expires_at)
    WHERE idempotency_key IS NOT NULL;
