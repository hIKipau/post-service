package usecase

import (
	"context"
	"fmt"
	"post-service/internal/domain"

	"github.com/google/uuid"
)

//- GetReplies

//	GetChildrenPosts(ctx context.Context, parentID uuid.UUID, pageSize, page int64) ([]domain.Post, error)

func (uc *Usecase) GetRepliesPosts(ctx context.Context, parentID uuid.UUID, pageSize, page int64) ([]domain.Post, error) {
	posts, err := uc.repo.GetChildrenPosts(ctx, parentID, pageSize, page)
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return posts, nil
	}

	postIDs := make([]uuid.UUID, len(posts))
	for i, post := range posts {
		postIDs[i] = post.ID
	}

	dislikes, err := uc.cache.GetPostsDislikes(ctx, postIDs)
	if err != nil {
		return nil, fmt.Errorf("get replies dislikes: %w", err)
	}
	likes, err := uc.cache.GetPostsLikes(ctx, postIDs)
	if err != nil {
		return nil, fmt.Errorf("get replies likes: %w", err)
	}

	for i := range posts {
		id := posts[i].ID
		posts[i].DislikeCount = dislikes[id]
		posts[i].LikeCount = likes[id]
		// ReplyCount is maintained transactionally by PostgreSQL.
	}

	return posts, nil
}
