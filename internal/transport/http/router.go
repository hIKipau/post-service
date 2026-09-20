package http

import (
	"log/slog"
	"net/http"

	"post-service/internal/transport/http/handler"
	"post-service/internal/transport/http/middleware"
)

// NewRouter protects all post routes with the existing JWT verifier.
func NewRouter(service handler.PostService, verifier middleware.TokenVerifier, logger *slog.Logger) http.Handler {
	h := handler.NewHandler(service, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /feed", h.GetFeed)
	mux.HandleFunc("POST /posts", h.CreatePost)
	mux.HandleFunc("PATCH /posts/{postID}", h.UpdatePost)
	mux.HandleFunc("DELETE /posts/{postID}", h.DeletePost)
	mux.HandleFunc("GET /posts/{postID}/replies", h.GetRepliesPosts)
	mux.HandleFunc("POST /posts/{postID}/replies", h.CreateReply)
	mux.HandleFunc("PUT /posts/{postID}/like", h.LikePost)
	mux.HandleFunc("DELETE /posts/{postID}/like", h.UnLikePost)
	mux.HandleFunc("PUT /posts/{postID}/dislike", h.DislikePost)
	mux.HandleFunc("DELETE /posts/{postID}/dislike", h.UnDislikePost)
	return middleware.Auth(verifier)(mux)
}
