package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestServeHTTPShutdownOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &http.Server{Addr: "127.0.0.1:0"}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = server.Close()
		t.Fatal("server failed to shut down")
	}
}

func TestServeHTTPReportsListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := &http.Server{Addr: listener.Addr().String()}
	if err := serveHTTP(context.Background(), server, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second); err == nil {
		t.Fatal("expected occupied address error")
	}
}

// TestServeListenerDrainsRequests covers cancellation, accept failures and forced shutdown.
func TestServeListenerDrainsRequests(t *testing.T) {
	for _, mode := range []string{"cancel", "listener failure", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			started, release, shuttingDown := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			finish := func() { releaseOnce.Do(func() { close(release) }) }
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-release:
					w.WriteHeader(http.StatusNoContent)
				case <-r.Context().Done():
				}
			})}
			server.RegisterOnShutdown(func() { close(shuttingDown) })
			t.Cleanup(func() { finish(); cancel(); _ = server.Close(); _ = listener.Close() })
			timeout := 2 * time.Second
			if mode == "timeout" {
				timeout = 50 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				done <- serveListener(ctx, server, listener, slog.New(slog.NewTextHandler(io.Discard, nil)), timeout)
			}()
			response := make(chan int, 1)
			client := &http.Client{Timeout: 3 * time.Second}
			go func() {
				res, err := client.Get("http://" + listener.Addr().String())
				if err != nil {
					response <- 0
					return
				}
				defer res.Body.Close()
				response <- res.StatusCode
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start")
			}
			if mode == "listener failure" {
				_ = listener.Close()
			} else {
				cancel()
			}
			select {
			case <-shuttingDown:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not start")
			}
			if mode != "timeout" {
				select {
				case err := <-done:
					t.Fatalf("server stopped before request finished: %v", err)
				default:
				}
				finish()
			}
			select {
			case err := <-done:
				if mode == "cancel" && err != nil {
					t.Fatal(err)
				}
				if mode == "listener failure" && err == nil {
					t.Fatal("listener error was lost")
				}
				if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected shutdown timeout, got %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("server did not stop")
			}
			select {
			case status := <-response:
				if mode != "timeout" && status != http.StatusNoContent {
					t.Fatalf("in-flight request interrupted: status=%d", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("client request did not finish")
			}
		})
	}
}
