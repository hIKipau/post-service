package usecase

import (
	"context"
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

	dislikes, _ := uc.cache.GetPostsDislikes(ctx, postIDs)
	likes, _ := uc.cache.GetPostsLikes(ctx, postIDs)
	replies, _ := uc.cache.GetPostsRepliesCount(ctx, postIDs)

	for i := range posts {
		id := posts[i].ID
		if dislikes != nil {
			posts[i].DislikeCount = dislikes[id]
		}
		if likes != nil {
			posts[i].LikeCount = likes[id]
		}
		if replies != nil {
			posts[i].ReplyCount = replies[id]
		}
	}

	return posts, nil
}
