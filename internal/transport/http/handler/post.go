package handler

import (
	"net/http"
	"time"

	"post-service/internal/domain"
	"post-service/internal/transport/http/middleware"

	"github.com/google/uuid"
)

// CreatePost validates the request text and creates a root post for the authenticated user.
func (h *Handler) CreatePost(w http.ResponseWriter, r *http.Request) {
	text, ok := readText(w, r)
	if !ok {
		return
	}
	h.savePost(w, r, newPost(r, text))
}

// UpdatePost validates the post ID and text, then requests an owner-only update and returns 204 on success.
func (h *Handler) UpdatePost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	text, ok := readText(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := h.service.UpdatePost(r.Context(), domain.Post{ID: id, AuthorID: userID, Text: text}); err != nil {
		h.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeletePost requests a soft deletion using the authenticated user's ID and returns 204 on success.
func (h *Handler) DeletePost(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	userID, _ := middleware.UserID(r.Context())
	if err := h.service.DeletePost(r.Context(), id, userID); err != nil {
		h.serviceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newPost builds an unsaved post with a generated UUID, authenticated author and current UTC timestamps.
func newPost(r *http.Request, text string) domain.Post {
	userID, _ := middleware.UserID(r.Context())
	now := time.Now().UTC()
	return domain.Post{ID: uuid.New(), AuthorID: userID, Text: text, CreatedAt: now, UpdatedAt: now}
}

// savePost passes a prepared post to the service and returns its JSON representation with status 201 on success.
func (h *Handler) savePost(w http.ResponseWriter, r *http.Request, post domain.Post) {
	if err := h.service.CreatePost(r.Context(), post); err != nil {
		h.serviceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, postDTO(post))
}
