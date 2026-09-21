-- +goose Up
CREATE TABLE posts (
    id            uuid        PRIMARY KEY,
    author_id     uuid        NOT NULL,
    text          text        NOT NULL,
    root_id       uuid,
    parent_id     uuid,
    reply_count   bigint      NOT NULL DEFAULT 0,
    like_count    bigint      NOT NULL DEFAULT 0,
    dislike_count bigint      NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz,

    CONSTRAINT posts_id_nonzero CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT posts_author_id_nonzero CHECK (author_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    -- Soft deletion clears the text, but keeps the row and its thread relationships.
    CONSTRAINT posts_text_length CHECK (
        char_length(text) <= 10000 AND (deleted_at IS NOT NULL OR char_length(text) > 0)
    ),
    CONSTRAINT posts_counts_nonnegative CHECK (
        reply_count >= 0 AND like_count >= 0 AND dislike_count >= 0
    ),
    -- Root posts have neither reference; replies must have both.
    CONSTRAINT posts_thread_references CHECK ((parent_id IS NULL) = (root_id IS NULL)),
    CONSTRAINT posts_parent_not_self CHECK (parent_id <> id),
    CONSTRAINT posts_root_not_self CHECK (root_id <> id),
    CONSTRAINT posts_parent_fk FOREIGN KEY (parent_id) REFERENCES posts (id) ON DELETE RESTRICT,
    CONSTRAINT posts_root_fk FOREIGN KEY (root_id) REFERENCES posts (id) ON DELETE RESTRICT
);

COMMENT ON TABLE posts IS 'Root posts and replies; deletion is soft and retains thread relationships.';
COMMENT ON COLUMN posts.author_id IS 'User UUID from the external authentication service; no local users table.';
COMMENT ON COLUMN posts.parent_id IS 'Direct parent of a reply; NULL for a root post.';
COMMENT ON COLUMN posts.root_id IS 'Original root post of a reply thread; NULL for a root post.';
COMMENT ON COLUMN posts.reply_count IS 'Active direct replies, maintained by repository transactions.';
COMMENT ON COLUMN posts.like_count IS 'Database snapshot used for reply ordering; live reactions are stored in Redis.';
COMMENT ON COLUMN posts.dislike_count IS 'Database snapshot; live reactions are stored in Redis.';

-- +goose Down
-- Destructive: rolling back this migration removes all posts and replies.
DROP TABLE posts;
