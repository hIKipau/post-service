package handler

import (
	"context"
	"net/http"

	"post-service/internal/transport/http/middleware"

	"github.com/google/uuid"
)

// LikePost requests a like for the authenticated user, replacing any existing dislike.
func (h *Handler) LikePost(w http.ResponseWriter, r *http.Request) {
	h.reaction(w, r, h.service.LikePost)
}

// UnLikePost requests removal of the authenticated user's like, including when it is already absent.
func (h *Handler) UnLikePost(w http.ResponseWriter, r *http.Request) {
	h.reaction(w, r, h.service.UnLikePost)
}

// DislikePost requests a dislike for the authenticated user, replacing any existing like.
func (h *Handler) DislikePost(w http.ResponseWriter, r *http.Request) {
	h.reaction(w, r, h.service.DislikePost)
}

// UnDislikePost requests removal of the authenticated user's dislike, including when it is already absent.
func (h *Handler) UnDislikePost(w http.ResponseWriter, r *http.Request) {
	h.reaction(w, r, h.service.UnDislikePost)
}

// reaction checks that the post is active, runs the action for the authenticated user and returns 204 on success.
func (h *Handler) reaction(w http.ResponseWriter, r *http.Request, action func(context.Context, uuid.UUID, uuid.UUID) error) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := h.service.GetPost(r.Context(), id); err != nil {
		h.serviceError(w, r, err)
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := action(r.Context(), id, userID); err != nil {
		h.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
