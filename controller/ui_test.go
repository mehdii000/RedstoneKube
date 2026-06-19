package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleUIServesPage(t *testing.T) {
	c, _ := newTestController(nil)
	rec := httptest.NewRecorder()
	c.handleUI(rec, httptest.NewRequest("GET", "/ui/", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "EventSource") || !strings.Contains(body, "/stream") {
		t.Error("page should wire EventSource to /stream")
	}
}
