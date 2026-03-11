CREATE TABLE IF NOT EXISTS quote_updates (
    id          UUID PRIMARY KEY,
    request_id  VARCHAR(64) UNIQUE,
    pair        VARCHAR(16) NOT NULL,
    status      VARCHAR(16) NOT NULL,
    CHECK (
        (status = 'pending' AND price IS NULL AND error IS NULL)
        OR (status = 'done' AND price IS NOT NULL AND error IS NULL)
        OR (status = 'failed' AND error IS NOT NULL AND price IS NULL)
    ),
    price       NUMERIC(18, 8),
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_quote_updates_latest_done
    ON quote_updates (pair, updated_at DESC) WHERE status = 'done';

CREATE TABLE IF NOT EXISTS outbox (
    update_id   UUID PRIMARY KEY REFERENCES quote_updates(id) ON DELETE CASCADE,
    pair        VARCHAR(16) NOT NULL,
    attempts    INT NOT NULL DEFAULT 0,
    claimed_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_created_at ON outbox (created_at);
CREATE INDEX IF NOT EXISTS idx_outbox_claimable ON outbox (created_at) WHERE claimed_at IS NULL;
