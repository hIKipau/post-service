-- +goose Up
CREATE TABLE post_reactions (
    post_id uuid NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    user_id uuid NOT NULL CHECK (user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    kind smallint NOT NULL CHECK (kind IN (-1, 1)),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (post_id, user_id)
);

-- Stored in the same transaction as the reaction and its aggregate counters.
-- Do not expire these receipts while the corresponding stream events can be replayed.
CREATE TABLE reaction_sync_events (
    event_id uuid PRIMARY KEY,
    outcome text NOT NULL DEFAULT 'applied' CHECK (outcome IN ('applied', 'post_missing')),
    processed_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE post_reactions IS 'Durable per-user reactions, asynchronously projected from Redis Streams.';
COMMENT ON TABLE reaction_sync_events IS 'Idempotency receipts for reaction events; missing posts are explicitly recorded.';
COMMENT ON COLUMN posts.like_count IS 'Like count maintained transactionally with post_reactions; Redis may be ahead during synchronization.';
COMMENT ON COLUMN posts.dislike_count IS 'Dislike count maintained transactionally with post_reactions; Redis may be ahead during synchronization.';

-- +goose Down
-- Destructive: durable reaction membership and deduplication receipts are lost.
DROP TABLE reaction_sync_events;
DROP TABLE post_reactions;
COMMENT ON COLUMN posts.like_count IS 'Database snapshot used for reply ordering; live reactions are stored in Redis.';
COMMENT ON COLUMN posts.dislike_count IS 'Database snapshot; live reactions are stored in Redis.';
