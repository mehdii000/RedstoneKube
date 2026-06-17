package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Controller owns the warm pool. Dependencies are func fields so tests inject fakes
// without interfaces or a real cluster.
type Controller struct {
	game, image string
	poolSize    int

	listPods     func(game string) ([]Pod, error)
	createPod    func(name string) error
	deletePod    func(name string) error
	setAllocated func(name string) error
	register     func(name, addr string) error
	unregister   func(name string) error

	mu         sync.Mutex // ponytail: one global lock; split per-game if many games churn concurrently
	registered map[string]bool
}

func (c *Controller) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Game string `json:"game"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if body.Game == "" {
		body.Game = c.game
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods(body.Game)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	p := pickAllocatable(pods, body.Game)
	if p == nil {
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

func (c *Controller) handleDone(w http.ResponseWriter, r *http.Request) {
	// path: /instances/{id}/done
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/instances/"), "/done")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "bad instance id", http.StatusBadRequest)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods(c.game)
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

// reconcile refills the pool and syncs Velocity registration. Called on a ticker.
func (c *Controller) reconcile() {
	c.mu.Lock()
	defer c.mu.Unlock()
	pods, err := c.listPods(c.game)
	if err != nil {
		log.Printf("reconcile: list: %v", err)
		return
	}
	// refill
	for i := 0; i < needed(pods, c.poolSize); i++ {
		name := fmt.Sprintf("mg-%s-%s", c.game, randSuffix())
		if err := c.createPod(name); err != nil {
			log.Printf("reconcile: create %s: %v", name, err)
		}
	}
	// registration sync: register newly-Ready, unregister vanished
	seen := map[string]bool{}
	for _, p := range pods {
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
}

func randSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() {
	game := envOr("GAME", "stub")
	image := envOr("MINIGAME_IMAGE", "mc/minigame-stub:dev")
	poolSize, _ := strconv.Atoi(envOr("POOL_SIZE", "1"))
	velBase := envOr("VELOCITY_REGISTER_URL", "http://velocity.mc.svc.cluster.local:8080")
	controllerURL := envOr("CONTROLLER_URL", "http://controller.mc.svc.cluster.local:8080")

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
		game: game, image: image, poolSize: poolSize,
		listPods:     k.listPods,
		createPod:    func(name string) error { return k.createPod(name, game, image, controllerURL) },
		deletePod:    k.deletePod,
		setAllocated: k.setAllocated,
		register:     v.register,
		unregister:   v.unregister,
		registered:   map[string]bool{},
	}

	go func() {
		for range time.Tick(2 * time.Second) {
			c.reconcile()
		}
	}()

	http.HandleFunc("/allocate", c.handleAllocate)
	http.HandleFunc("/instances/", c.handleDone)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	log.Printf("controller up: game=%s pool=%d", game, poolSize)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
