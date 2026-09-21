-- +goose Up
-- GetFeedCandidates: newest active posts; author exclusion remains a filter.
CREATE INDEX posts_feed_idx ON posts (created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- GetChildrenPosts: direct active replies in the repository's exact sort order.
CREATE INDEX posts_replies_idx ON posts (parent_id, like_count DESC, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- Include soft-deleted replies so foreign-key checks can find every reference.
CREATE INDEX posts_parent_id_idx ON posts (parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX posts_root_id_idx ON posts (root_id) WHERE root_id IS NOT NULL;

-- +goose Down
DROP INDEX posts_root_id_idx;
DROP INDEX posts_parent_id_idx;
DROP INDEX posts_replies_idx;
DROP INDEX posts_feed_idx;
