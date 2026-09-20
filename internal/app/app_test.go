package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPShutdownOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := &http.Server{Addr: "127.0.0.1:0"}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
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
	if err := serveHTTP(context.Background(), server, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("expected occupied address error")
	}
}
