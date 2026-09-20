package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"post-service/internal/domain"
	"post-service/internal/security/jwt"
	"post-service/internal/transport/http/handler"
)

type verifierStub struct{ user uuid.UUID }

func (v verifierStub) Verify(string) (*jwt.Claims, error) { return &jwt.Claims{UserID: v.user}, nil }

type serviceStub struct {
	handler.PostService
	create  func(domain.Post) error
	update  func(domain.Post) error
	delete  func(uuid.UUID, uuid.UUID) error
	get     func(uuid.UUID) (domain.Post, error)
	feed    func(uuid.UUID) ([]domain.Post, error)
	replies func(uuid.UUID, int64, int64) ([]domain.Post, error)
	react   func(string, uuid.UUID, uuid.UUID) error
}

func (s *serviceStub) CreatePost(_ context.Context, p domain.Post) error            { return s.create(p) }
func (s *serviceStub) UpdatePost(_ context.Context, p domain.Post) error            { return s.update(p) }
func (s *serviceStub) DeletePost(_ context.Context, p, u uuid.UUID) error           { return s.delete(p, u) }
func (s *serviceStub) GetPost(_ context.Context, id uuid.UUID) (domain.Post, error) { return s.get(id) }
func (s *serviceStub) GetFeed(_ context.Context, u uuid.UUID) ([]domain.Post, error) {
	return s.feed(u)
}
func (s *serviceStub) GetRepliesPosts(_ context.Context, p uuid.UUID, size, page int64) ([]domain.Post, error) {
	return s.replies(p, size, page)
}
func (s *serviceStub) LikePost(_ context.Context, p, u uuid.UUID) error { return s.react("like", p, u) }
func (s *serviceStub) UnLikePost(_ context.Context, p, u uuid.UUID) error {
	return s.react("unlike", p, u)
}
func (s *serviceStub) DislikePost(_ context.Context, p, u uuid.UUID) error {
	return s.react("dislike", p, u)
}
func (s *serviceStub) UnDislikePost(_ context.Context, p, u uuid.UUID) error {
	return s.react("undislike", p, u)
}

func testRouter(s handler.PostService, user uuid.UUID) http.Handler {
	return NewRouter(s, verifierStub{user}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCreatePostAndNestedReply(t *testing.T) {
	user, parent, root := uuid.New(), uuid.New(), uuid.New()
	for _, mode := range []string{"post", "reply", "nested reply"} {
		t.Run(mode, func(t *testing.T) {
			var saved domain.Post
			s := &serviceStub{create: func(p domain.Post) error { saved = p; return nil }, get: func(id uuid.UUID) (domain.Post, error) {
				if id != parent {
					t.Fatalf("unexpected parent %v", id)
				}
				p := domain.Post{ID: parent}
				if mode == "nested reply" {
					p.RootID = &root
				}
				return p, nil
			}}
			path := "/posts"
			if mode != "post" {
				path += "/" + parent.String() + "/replies"
			}
			w := request(testRouter(s, user), "POST", path, `{"text":"Привет"}`)
			if w.Code != 201 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if saved.ID == uuid.Nil || saved.AuthorID != user || saved.CreatedAt.IsZero() || saved.UpdatedAt != saved.CreatedAt || saved.Text != "Привет" {
				t.Fatalf("invalid generated post: %+v", saved)
			}
			if mode == "post" {
				if saved.ParentID != nil || saved.RootID != nil {
					t.Fatal("root post has parent")
				}
			} else {
				wantRoot := parent
				if mode == "nested reply" {
					wantRoot = root
				}
				if saved.ParentID == nil || *saved.ParentID != parent || saved.RootID == nil || *saved.RootID != wantRoot {
					t.Fatalf("invalid thread: %+v", saved)
				}
			}
			var dto struct {
				ID uuid.UUID `json:"id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil || dto.ID != saved.ID {
				t.Fatalf("response=%s err=%v", w.Body, err)
			}
		})
	}
}

func TestInputValidation(t *testing.T) {
	id := uuid.New().String()
	for _, tt := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"unknown author", "POST", "/posts", `{"text":"ok","author_id":"spoof"}`, 400},
		{"forged counts", "POST", "/posts", `{"text":"ok","like_count":999}`, 400},
		{"forged root", "POST", "/posts", `{"text":"ok","root_id":"spoof"}`, 400},
		{"blank", "POST", "/posts", `{"text":"  "}`, 400},
		{"null", "POST", "/posts", `null`, 400},
		{"empty", "POST", "/posts", ``, 400},
		{"malformed", "POST", "/posts", `{"text":`, 400},
		{"trailing value", "POST", "/posts", `{"text":"ok"} {}`, 400},
		{"too much text", "POST", "/posts", `{"text":"` + strings.Repeat("я", 10001) + `"}`, 400},
		{"too large", "POST", "/posts", `{"text":"` + strings.Repeat("x", 64<<10) + `"}`, 413},
		{"bad uuid", "DELETE", "/posts/nope", ``, 400},
		{"zero uuid", "PUT", "/posts/" + uuid.Nil.String() + "/like", ``, 400},
		{"negative page", "GET", "/posts/" + id + "/replies?page=-1", ``, 400},
		{"zero size", "GET", "/posts/" + id + "/replies?page_size=0", ``, 400},
		{"excess size", "GET", "/posts/" + id + "/replies?page_size=101", ``, 400},
		{"overflow", "GET", "/posts/" + id + "/replies?page=9223372036854775807", ``, 400},
		{"duplicate page", "GET", "/posts/" + id + "/replies?page=0&page=1", ``, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := request(testRouter(&serviceStub{}, uuid.New()), tt.method, tt.path, tt.body)
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	h := testRouter(&serviceStub{}, uuid.New())
	r := httptest.NewRequest("POST", "/posts", strings.NewReader(`{"text":"ok"}`))
	r.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("missing content type: %d", w.Code)
	}
}

func TestMutationIdentityAndErrorMapping(t *testing.T) {
	user, id := uuid.New(), uuid.New()
	for _, mode := range []string{"update", "delete", "like", "unlike", "dislike", "undislike"} {
		t.Run(mode, func(t *testing.T) {
			called := false
			check := func(p, u uuid.UUID) error {
				called = true
				if p != id || u != user {
					t.Fatalf("wrong identity %v/%v", p, u)
				}
				return nil
			}
			s := &serviceStub{
				get: func(p uuid.UUID) (domain.Post, error) { return domain.Post{ID: p}, nil },
				update: func(p domain.Post) error {
					if p.Text != "edit" {
						t.Fatal("wrong text")
					}
					return check(p.ID, p.AuthorID)
				},
				delete: check,
				react: func(action string, p, u uuid.UUID) error {
					if action != mode {
						t.Fatal(action)
					}
					return check(p, u)
				},
			}
			method, path, body := "DELETE", "/posts/"+id.String(), ""
			switch mode {
			case "update":
				method, body = "PATCH", `{"text":"edit"}`
			case "like", "dislike":
				method = "PUT"
				path += "/" + mode
			case "unlike":
				path += "/like"
			case "undislike":
				path += "/dislike"
			}
			w := request(testRouter(s, user), method, path, body)
			if w.Code != 204 || !called || w.Body.Len() != 0 {
				t.Fatalf("status=%d called=%v body=%s", w.Code, called, w.Body)
			}
		})
	}
	for _, tt := range []struct {
		err    error
		status int
	}{
		{fmt.Errorf("wrapped: %w", domain.ErrPostNotFound), 404},
		{errors.New("secret database connection details"), 500},
	} {
		s := &serviceStub{delete: func(uuid.UUID, uuid.UUID) error { return tt.err }, get: func(uuid.UUID) (domain.Post, error) { return domain.Post{}, tt.err }}
		for _, route := range []struct{ method, path string }{{"DELETE", "/posts/" + id.String()}, {"PUT", "/posts/" + id.String() + "/like"}, {"POST", "/posts/" + id.String() + "/replies"}} {
			w := request(testRouter(s, user), route.method, route.path, `{"text":"reply"}`)
			if w.Code != tt.status || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		}
	}
}

func TestFeedAndReplies(t *testing.T) {
	user, id := uuid.New(), uuid.New()
	now := time.Now()
	s := &serviceStub{
		feed: func(u uuid.UUID) ([]domain.Post, error) {
			if u != user {
				t.Fatal("wrong user")
			}
			return nil, nil
		},
		get: func(uuid.UUID) (domain.Post, error) { return domain.Post{ID: id}, nil },
		replies: func(p uuid.UUID, size, page int64) ([]domain.Post, error) {
			if p != id || size != 5 || page != 2 {
				t.Fatalf("pagination=%v/%d/%d", p, size, page)
			}
			return []domain.Post{{ID: id, ReplyCount: 3}, {ID: uuid.New(), DeletedAt: &now}}, nil
		},
	}
	h := testRouter(s, user)
	w := request(h, "GET", "/feed", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("feed=%d %s", w.Code, w.Body)
	}
	w = request(h, "GET", "/posts/"+id.String()+"/replies?page_size=5&page=2", "")
	var posts []struct {
		ReplyCount int64 `json:"reply_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &posts); err != nil || w.Code != 200 || len(posts) != 1 || posts[0].ReplyCount != 3 {
		t.Fatalf("replies=%d %s err=%v", w.Code, w.Body, err)
	}
}

func TestHeadDoesNotConsumeFeed(t *testing.T) {
	w := request(testRouter(&serviceStub{}, uuid.New()), "HEAD", "/feed", "")
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
		t.Fatalf("status=%d allow=%s", w.Code, w.Header().Get("Allow"))
	}
}

func TestAllRoutesRequireAuth(t *testing.T) {
	h := testRouter(&serviceStub{}, uuid.New())
	for _, route := range []struct{ method, path string }{
		{"GET", "/feed"}, {"POST", "/posts"}, {"PATCH", "/posts/x"}, {"DELETE", "/posts/x"},
		{"GET", "/posts/x/replies"}, {"POST", "/posts/x/replies"},
		{"PUT", "/posts/x/like"}, {"DELETE", "/posts/x/like"}, {"PUT", "/posts/x/dislike"}, {"DELETE", "/posts/x/dislike"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != 401 {
			t.Fatalf("%s %s status=%d", route.method, route.path, w.Code)
		}
	}
}
