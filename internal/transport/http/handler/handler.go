package handler

import (
	"context"
	"log/slog"

	"post-service/internal/domain"

	"github.com/google/uuid"
)

type PostService interface {
	GetFeed(context.Context, uuid.UUID) ([]domain.Post, error)
	GetPost(context.Context, uuid.UUID) (domain.Post, error)
	CreatePost(context.Context, domain.Post) error
	UpdatePost(context.Context, domain.Post) error
	DeletePost(context.Context, uuid.UUID, uuid.UUID) error
	GetRepliesPosts(context.Context, uuid.UUID, int64, int64) ([]domain.Post, error)
	LikePost(context.Context, uuid.UUID, uuid.UUID) error
	UnLikePost(context.Context, uuid.UUID, uuid.UUID) error
	DislikePost(context.Context, uuid.UUID, uuid.UUID) error
	UnDislikePost(context.Context, uuid.UUID, uuid.UUID) error
}

type Handler struct {
	service PostService
	logger  *slog.Logger
}

// NewHandler creates HTTP handlers backed by the post service, using the default logger if none is provided.
func NewHandler(service PostService, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}
