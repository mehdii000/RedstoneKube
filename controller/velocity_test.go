package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVelRegister(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	v := newVel(srv.URL, "sekret")
	if err := v.register("mg-stub-abc", "10.0.0.5:25565"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/servers" {
		t.Errorf("register hit %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("auth = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "mg-stub-abc") || !strings.Contains(gotBody, "10.0.0.5:25565") {
		t.Errorf("body = %q", gotBody)
	}
}

func TestVelUnregister(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if err := newVel(srv.URL, "x").unregister("mg-stub-abc"); err != nil {
		t.Fatal(err)
	}
	if gotMethod != "DELETE" || gotPath != "/servers/mg-stub-abc" {
		t.Errorf("unregister hit %s %s", gotMethod, gotPath)
	}
}
