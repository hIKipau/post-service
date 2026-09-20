package handler

import (
	"math"
	"net/http"
)

// GetRepliesPosts validates pagination, checks that the parent post is active and returns a page of direct replies.
func (h *Handler) GetRepliesPosts(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	page, ok := queryInt(w, r, "page", 0, 0, math.MaxInt64)
	if !ok {
		return
	}
	size, ok := queryInt(w, r, "page_size", 20, 1, 100)
	if !ok {
		return
	}
	if page > math.MaxInt64/size {
		writeError(w, http.StatusBadRequest, "page is too large")
		return
	}
	if _, err := h.service.GetPost(r.Context(), id); err != nil {
		h.serviceError(w, r, err)
		return
	}
	posts, err := h.service.GetRepliesPosts(r.Context(), id, size, page)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}
	writePosts(w, posts)
}

// CreateReply validates the text and parent post, derives the thread's root ID and saves the reply.
func (h *Handler) CreateReply(w http.ResponseWriter, r *http.Request) {
	text, ok := readText(w, r)
	if !ok {
		return
	}
	post := newPost(r, text)
	parentID, ok := pathID(w, r)
	if !ok {
		return
	}
	parent, err := h.service.GetPost(r.Context(), parentID)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}
	rootID := parent.ID
	if parent.RootID != nil {
		rootID = *parent.RootID
	}
	post.ParentID, post.RootID = &parentID, &rootID
	h.savePost(w, r, post)
}
