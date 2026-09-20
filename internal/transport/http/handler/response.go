package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"post-service/internal/domain"

	"github.com/google/uuid"
)

// Transport DTO keeps the wire format independent of Go field names.
type postResponse struct {
	ID           uuid.UUID  `json:"id"`
	AuthorID     uuid.UUID  `json:"author_id"`
	Text         string     `json:"text"`
	RootID       *uuid.UUID `json:"root_id"`
	ParentID     *uuid.UUID `json:"parent_id"`
	ReplyCount   int64      `json:"reply_count"`
	LikeCount    int64      `json:"like_count"`
	DislikeCount int64      `json:"dislike_count"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// postDTO copies the public fields of a domain post into an HTTP response DTO.
func postDTO(p domain.Post) postResponse {
	return postResponse{ID: p.ID, AuthorID: p.AuthorID, Text: p.Text,
		RootID: p.RootID, ParentID: p.ParentID, ReplyCount: p.ReplyCount,
		LikeCount: p.LikeCount, DislikeCount: p.DislikeCount,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

// writePosts returns non-deleted posts as a JSON array with status 200, using [] for an empty result.
func writePosts(w http.ResponseWriter, posts []domain.Post) {
	result := make([]postResponse, 0, len(posts))
	for _, post := range posts {
		if post.DeletedAt == nil {
			result = append(result, postDTO(post))
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// serviceError returns 404 for a missing post; other errors are logged and returned as a generic 500 response.
func (h *Handler) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, domain.ErrPostNotFound) {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	h.logger.Error("HTTP request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

// writeError sends the given status and a JSON object containing the error message.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON sets the JSON content type and HTTP status, then encodes value into the response body.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
