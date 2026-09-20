package handler

import (
	"net/http"

	"post-service/internal/transport/http/middleware"
)

// GetFeed returns the authenticated user's feed as JSON and rejects HEAD requests to avoid consuming the feed.
func (h *Handler) GetFeed(w http.ResponseWriter, r *http.Request) {
	// ServeMux also matches HEAD to GET, but reading a feed consumes its queue.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, _ := middleware.UserID(r.Context())
	posts, err := h.service.GetFeed(r.Context(), userID)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}
	writePosts(w, posts)
}
