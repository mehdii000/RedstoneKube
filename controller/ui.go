package main

import (
	_ "embed"
	"net/http"
)

//go:embed index.html
var indexHTML []byte

func (c *Controller) handleUI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}
