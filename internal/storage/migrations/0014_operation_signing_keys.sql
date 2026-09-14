-- Server-issued operation idempotency credentials use persisted HMAC keys.
-- Rotated keys remain readable until their retention deadline so credentials
-- issued before restart continue to resolve their original binding. Revoked
-- keys fail closed immediately.
CREATE TABLE operation_signing_keys (
    id             TEXT PRIMARY KEY,
    secret         TEXT NOT NULL UNIQUE,
    state          TEXT NOT NULL CHECK (state IN ('active','retired','revoked')),
    created_at     INTEGER NOT NULL,
    rotated_at     INTEGER,
    retained_until INTEGER
);

CREATE INDEX operation_signing_keys_state_idx
    ON operation_signing_keys(state, retained_until);
