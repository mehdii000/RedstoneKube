package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// vel calls the velocity-register plugin to add/remove backends live.
type vel struct {
	base, token string
	hc          *http.Client
}

func newVel(base, token string) *vel {
	return &vel{base: base, token: token, hc: &http.Client{Timeout: 5 * time.Second}}
}

func (v *vel) req(method, path, body string) error {
	req, err := http.NewRequest(method, v.base+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+v.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("velocity-register %s %s: %d", method, path, resp.StatusCode)
	}
	return nil
}

// register is an idempotent upsert; re-registering the same name updates it.
func (v *vel) register(name, addr string) error {
	return v.req("POST", "/servers", fmt.Sprintf(`{"name":%q,"address":%q}`, name, addr))
}

func (v *vel) unregister(name string) error {
	return v.req("DELETE", "/servers/"+name, "")
}
