package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamSendsInitialSnapshot(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true}})
	srv := httptest.NewServer(http.HandlerFunc(c.handleStream))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	// Read the first SSE data line, then bail — the stream is infinite.
	done := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data:") {
				done <- line
				return
			}
		}
		done <- ""
	}()
	select {
	case line := <-done:
		if !strings.Contains(line, "\"instances\"") {
			t.Errorf("first event missing snapshot: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE event within 2s")
	}
}
