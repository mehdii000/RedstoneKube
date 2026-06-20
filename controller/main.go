package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Controller owns the warm pool across all configured games. Dependencies are
// func fields so tests inject fakes without interfaces or a real cluster.
type Controller struct {
	games []Game

	listPods     func(game string) ([]Pod, error)
	createPod    func(name, game, image string) error
	deletePod    func(name string) error
	setAllocated func(name string) error
	register     func(name, addr string) error
	unregister   func(name string) error

	mu         sync.Mutex // ponytail: one global lock; split per-game if many games churn concurrently
	registered map[string]bool

	metrics      *metricsCache
	allocFails   map[string]int
	fetchMetrics func(ip string) (Metrics, bool) // injectable for tests
	podLogs      func(name string, tail int) (string, error)

	emptySince  map[string]time.Time // name -> first seen allocated+empty
	idleTimeout time.Duration        // reap allocated+empty instances after this
}

func (c *Controller) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Game string `json:"game"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Game == "" {
		http.Error(w, "game required", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if findGame(c.games, body.Game) == nil {
		http.Error(w, "unknown game", http.StatusNotFound)
		return
	}
	pods, err := c.listPods(body.Game)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	p := pickAllocatable(pods, body.Game)
	if p == nil {
		c.allocFails[body.Game]++
		http.Error(w, "no ready instance", http.StatusServiceUnavailable)
		return
	}
	if err := c.setAllocated(p.Name); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	json.NewEncoder(w).Encode(map[string]string{
		"server":  p.Name,
		"address": p.IP + ":25565",
	})
}

// handleInstances routes /instances/{id}/{done|logs}.
func (c *Controller) handleInstances(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/instances/")
	switch {
	case strings.HasSuffix(rest, "/done"):
		c.handleDone(w, r)
	case strings.HasSuffix(rest, "/logs"):
		c.handleLogs(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (c *Controller) handleLogs(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/instances/"), "/logs")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad instance id", http.StatusBadRequest)
		return
	}
	tail := 200
	if v := r.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	out, err := c.podLogs(id, tail)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(out))
}

func (c *Controller) handleDone(w http.ResponseWriter, r *http.Request) {
	// path: /instances/{id}/done
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/instances/"), "/done")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad instance id", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods("") // all minigames — id may belong to any game
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	known := false
	for _, p := range pods {
		if p.Name == id {
			known = true
		}
	}
	if !known {
		http.Error(w, "unknown instance", http.StatusNotFound)
		return
	}
	if err := c.unregister(id); err != nil {
		log.Printf("done: unregister %s: %v", id, err)
	}
	delete(c.registered, id)
	if err := c.deletePod(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// reconcile refills every game's pool and syncs Velocity registration. On a ticker.
func (c *Controller) reconcile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var all []Pod
	for _, g := range c.games {
		pods, err := c.listPods(g.Name)
		if err != nil {
			log.Printf("reconcile: list %s: %v", g.Name, err)
			continue
		}
		for i := 0; i < needed(pods, g.PoolSize); i++ {
			name := fmt.Sprintf("mg-%s-%s", g.Name, randSuffix())
			if err := c.createPod(name, g.Name, g.Image); err != nil {
				log.Printf("reconcile: create %s: %v", name, err)
			}
		}
		all = append(all, pods...)
	}
	// registration sync across all games: register newly-Ready, unregister vanished.
	seen := map[string]bool{}
	for _, p := range all {
		seen[p.Name] = true
		if p.Ready && !c.registered[p.Name] {
			if err := c.register(p.Name, p.IP+":25565"); err != nil {
				log.Printf("reconcile: register %s: %v", p.Name, err)
				continue
			}
			c.registered[p.Name] = true
		}
	}
	for name := range c.registered {
		if !seen[name] {
			if err := c.unregister(name); err != nil {
				log.Printf("reconcile: unregister %s: %v", name, err)
			}
			delete(c.registered, name)
		}
	}
	// reap abandoned (allocated, empty past idleTimeout) instances; warm pool untouched.
	fresh := func(n string) (Metrics, bool) { return c.metrics.get(n, metricsMaxAge) }
	for _, name := range idleReap(all, fresh, c.emptySince, time.Now(), c.idleTimeout) {
		if err := c.unregister(name); err != nil {
			log.Printf("reap: unregister %s: %v", name, err)
		}
		delete(c.registered, name)
		if err := c.deletePod(name); err != nil {
			log.Printf("reap: delete %s: %v", name, err)
		}
	}
}

func randSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() {
	velBase := envOr("VELOCITY_REGISTER_URL", "http://velocity.mc.svc.cluster.local:8080")
	controllerURL := envOr("CONTROLLER_URL", "http://controller.mc.svc.cluster.local:8080")
	gamesPath := envOr("GAMES_CONFIG", "/config/games.json")

	gamesJSON, err := os.ReadFile(gamesPath)
	if err != nil {
		log.Fatalf("read games config %s: %v", gamesPath, err)
	}
	games, err := parseGames(gamesJSON)
	if err != nil {
		log.Fatalf("games config: %v", err)
	}

	k, err := newKube()
	if err != nil {
		log.Fatalf("k8s: %v", err)
	}
	token, err := os.ReadFile("/secret/controller.token")
	if err != nil {
		log.Fatalf("read controller token: %v", err)
	}
	v := newVel(velBase, strings.TrimSpace(string(token)))

	c := &Controller{
		games:        games,
		listPods:     k.listPods,
		createPod:    func(name, game, image string) error { return k.createPod(name, game, image, controllerURL) },
		deletePod:    k.deletePod,
		setAllocated: k.setAllocated,
		register:     v.register,
		unregister:   v.unregister,
		registered:   map[string]bool{},
		metrics:      newMetricsCache(),
		allocFails:   map[string]int{},
		podLogs:      k.podLogs,
		emptySince:   map[string]time.Time{},
		idleTimeout:  parseIdleTimeout(envOr("IDLE_TIMEOUT", "5m")),
	}
	c.fetchMetrics = httpFetchMetrics(&http.Client{Timeout: 2 * time.Second})

	go func() {
		for range time.Tick(2 * time.Second) {
			c.reconcile()
		}
	}()
	go func() {
		for range time.Tick(2 * time.Second) {
			c.scrape()
		}
	}()

	http.HandleFunc("/allocate", c.handleAllocate)
	http.HandleFunc("/instances/", c.handleInstances)
	http.HandleFunc("/snapshot", c.handleSnapshot)
	http.HandleFunc("/stream", c.handleStream)
	http.HandleFunc("/ui/", c.handleUI)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	log.Printf("controller up: %d games", len(c.games))
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// scrape pulls /metrics from every Ready pod (short timeout) into the cache.
// Runs on the same cadence as reconcile; failures just leave the entry stale.
func (c *Controller) scrape() {
	pods, err := c.listPods("")
	if err != nil {
		log.Printf("scrape: list: %v", err)
		return
	}
	for _, p := range pods {
		if p.IP == "" {
			continue
		}
		if m, ok := c.fetchMetrics(p.IP); ok {
			c.metrics.put(p.Name, m)
		}
	}
}

// httpFetchMetrics is the production fetchMetrics: GET http://<ip>:9100/metrics.
func httpFetchMetrics(hc *http.Client) func(string) (Metrics, bool) {
	return func(ip string) (Metrics, bool) {
		resp, err := hc.Get("http://" + ip + ":9100/metrics")
		if err != nil {
			return Metrics{}, false
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return Metrics{}, false
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return Metrics{}, false
		}
		m, err := parseMetrics(b)
		if err != nil {
			return Metrics{}, false
		}
		return m, true
	}
}

// parseIdleTimeout reads IDLE_TIMEOUT (a Go duration); falls back to 5m on a bad value.
func parseIdleTimeout(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		log.Printf("IDLE_TIMEOUT %q invalid, using 5m", s)
		return 5 * time.Minute
	}
	return d
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
