# Slice 2 — Minigame Convention + Multi-Game Controller Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Generalize the controller from one hardcoded game to a config-driven set, and prove it with a procedurally-generated parkour game that follows a written image convention.

**Architecture:** The controller reads a `minigames` ConfigMap (JSON) listing `{name, image, poolSize}` per game and runs the Slice 1 warm-pool loop for each. A new parkour minigame generates a void world + course at runtime from a pure, seedable function. The lobby compass gains a parkour entry (config only). No generator/scaffolding — the convention is documented and demonstrated.

**Tech Stack:** Go (stdlib only, no client-go), Java 21 Paper plugins (dockerized Gradle), Kubernetes manifests, Make.

## Global Constraints

- Controller: **Go stdlib only — no client-go, no new deps.** Talks to the k8s API over the in-cluster REST client already in `controller/k8s.go`.
- Plugins: `compileOnly 'io.papermc.paper:paper-api:1.21.8-R0.1-SNAPSHOT'`; testable logic lives in **Bukkit-free** classes (mirror `mc.stub.Done`, `mc.lobby.Menu`).
- Pod labels are fixed: `app=minigame, game=<g>, alloc=<true|false>`. Pod name `mg-<game>-<rand>`.
- Game-over contract unchanged: pod POSTs `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done`.
- Backends are `FROM mc/mc-base:dev` (boots `online-mode=false` for Velocity modern forwarding).
- Tests: Go `testing` (no framework); Java JUnit 5 (`useJUnitPlatform`). No new test deps.
- Commit after every task.

---

### Task 1: Games config — pure parse + lookup (`controller/games.go`)

**Files:**
- Create: `controller/games.go`
- Test: `controller/games_test.go`

**Interfaces:**
- Produces: `type Game struct { Name, Image string; PoolSize int }`; `func parseGames(b []byte) ([]Game, error)`; `func findGame(games []Game, name string) *Game`.
- Consumes: nothing.

- [ ] **Step 1: Write the failing test**

```go
// controller/games_test.go
package main

import "testing"

func TestParseGames(t *testing.T) {
	in := []byte(`[{"name":"stub","image":"mc/minigame-stub:dev","poolSize":1},
	               {"name":"parkour","image":"mc/minigame-parkour:dev","poolSize":2}]`)
	g, err := parseGames(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(g) != 2 {
		t.Fatalf("len = %d, want 2", len(g))
	}
	if g[1].Name != "parkour" || g[1].Image != "mc/minigame-parkour:dev" || g[1].PoolSize != 2 {
		t.Errorf("game[1] = %+v", g[1])
	}
}

func TestParseGamesRejectsEmpty(t *testing.T) {
	if _, err := parseGames([]byte(`[]`)); err == nil {
		t.Error("want error on empty games list")
	}
	if _, err := parseGames([]byte(`not json`)); err == nil {
		t.Error("want error on bad json")
	}
}

func TestFindGame(t *testing.T) {
	games := []Game{{Name: "stub"}, {Name: "parkour"}}
	if findGame(games, "parkour") == nil {
		t.Error("parkour should be found")
	}
	if findGame(games, "ghost") != nil {
		t.Error("ghost should not be found")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd controller && go test -run 'Games|FindGame' ./...`
Expected: FAIL — `undefined: parseGames` / `undefined: Game` / `undefined: findGame`.

- [ ] **Step 3: Write minimal implementation**

```go
// controller/games.go
package main

import (
	"encoding/json"
	"fmt"
)

// Game is one configured minigame type: which image to run and how many warm
// pods to keep ready. Loaded from the minigames ConfigMap (JSON).
type Game struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	PoolSize int    `json:"poolSize"`
}

// parseGames decodes the games config JSON. An empty list is rejected — a
// controller with zero games is always a misconfiguration.
func parseGames(b []byte) ([]Game, error) {
	var games []Game
	if err := json.Unmarshal(b, &games); err != nil {
		return nil, err
	}
	if len(games) == 0 {
		return nil, fmt.Errorf("games config is empty")
	}
	return games, nil
}

// findGame returns the configured game by name, or nil if not configured.
func findGame(games []Game, name string) *Game {
	for i := range games {
		if games[i].Name == name {
			return &games[i]
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd controller && go test -run 'Games|FindGame' ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add controller/games.go controller/games_test.go
git commit -m "feat(controller): parse minigames config (games.go)"
```

---

### Task 2: Controller multi-game refactor (`main.go` + `k8s.go` + tests)

This is one cohesive change: the `Controller` struct goes from a single game to `games []Game`, which touches both handlers, `reconcile`, `main`, and the test helper at once (Go won't compile partially). `listPods("")` gains an all-minigames mode for `handleDone`.

**Files:**
- Modify: `controller/k8s.go:127-136` (`listPods` selector)
- Modify: `controller/main.go` (struct, handlers, reconcile, main)
- Test: `controller/server_test.go` (update helper + add two tests)

**Interfaces:**
- Consumes: `Game`, `findGame` (Task 1); `needed`, `pickAllocatable` (existing `pool.go`).
- Produces: `Controller{ games []Game; createPod func(name, game, image string) error; ... }`; `handleAllocate` returns 400 (missing game), 404 (unknown game), 503 (none ready); `handleDone` lists all minigames via `listPods("")`.

- [ ] **Step 1: Make `listPods("")` mean all minigames**

Replace `controller/k8s.go` lines 127-136 (the `listPods` func) with:

```go
// listPods returns minigame pods, parsed into the controller's Pod view.
// An empty game returns ALL minigames (used by handleDone to validate any id).
func (k *kube) listPods(game string) ([]Pod, error) {
	sel := "app%3Dminigame"
	if game != "" {
		sel += ",game%3D" + game
	}
	b, code, err := k.do("GET", "/api/v1/namespaces/"+namespace+"/pods?labelSelector="+sel, "", "")
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("listPods: %d %s", code, b)
	}
	return parsePodList(b)
}
```

- [ ] **Step 2: Update the test helper + add the new tests (failing)**

Replace the `newTestController` helper at the top of `controller/server_test.go` with this version (note: `game/image/poolSize` fields are gone; `games` and a 3-arg `createPod`), and append the two new tests at the end of the file:

```go
func newTestController(pods []Pod) (*Controller, *struct {
	sync.Mutex
	created, allocated, deleted, unregistered []string
}) {
	rec := &struct {
		sync.Mutex
		created, allocated, deleted, unregistered []string
	}{}
	c := &Controller{
		games: []Game{{Name: "stub", Image: "mc/minigame-stub:dev", PoolSize: 1}},
		listPods: func(game string) ([]Pod, error) {
			if game == "" {
				return pods, nil
			}
			var out []Pod
			for _, p := range pods {
				if p.Game == game {
					out = append(out, p)
				}
			}
			return out, nil
		},
		createPod: func(name, game, image string) error {
			rec.Lock()
			rec.created = append(rec.created, name+"|"+game+"|"+image)
			rec.Unlock()
			return nil
		},
		setAllocated: func(n string) error {
			rec.Lock()
			rec.allocated = append(rec.allocated, n)
			rec.Unlock()
			return nil
		},
		deletePod: func(n string) error {
			rec.Lock()
			rec.deleted = append(rec.deleted, n)
			rec.Unlock()
			return nil
		},
		register: func(string, string) error { return nil },
		unregister: func(n string) error {
			rec.Lock()
			rec.unregistered = append(rec.unregistered, n)
			rec.Unlock()
			return nil
		},
		registered: map[string]bool{},
	}
	return c, rec
}
```

Append these tests:

```go
func TestAllocateUnknownGame404(t *testing.T) {
	c, _ := newTestController([]Pod{{Name: "mg-stub-1", Game: "stub", Ready: true, IP: "10.0.0.5"}})
	r := httptest.NewRequest("POST", "/allocate", strings.NewReader(`{"game":"ghost"}`))
	w := httptest.NewRecorder()
	c.handleAllocate(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestReconcileRefillsEachGame(t *testing.T) {
	// Two games configured, zero pods existing -> create one of each.
	c, rec := newTestController(nil)
	c.games = []Game{
		{Name: "stub", Image: "mc/minigame-stub:dev", PoolSize: 1},
		{Name: "parkour", Image: "mc/minigame-parkour:dev", PoolSize: 1},
	}
	c.reconcile()
	rec.Lock()
	defer rec.Unlock()
	if len(rec.created) != 2 {
		t.Fatalf("created = %v, want one per game", rec.created)
	}
	joined := strings.Join(rec.created, " ")
	if !strings.Contains(joined, "|stub|mc/minigame-stub:dev") ||
		!strings.Contains(joined, "|parkour|mc/minigame-parkour:dev") {
		t.Errorf("created with wrong game/image: %v", rec.created)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail to compile**

Run: `cd controller && go test ./...`
Expected: FAIL — build errors (`unknown field 'games'`, `createPod` arity, `c.game` undefined) until `main.go` is refactored.

- [ ] **Step 4: Refactor `controller/main.go`**

Replace the `Controller` struct (lines 19-32) with:

```go
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
}
```

Replace `handleAllocate` (lines 34-62) with:

```go
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
```

In `handleDone` (lines 64-97), change the pod listing from `c.listPods(c.game)` to all minigames:

```go
	pods, err := c.listPods("") // all minigames — id may belong to any game
```

Replace `reconcile` (lines 99-135) with:

```go
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
}
```

Replace the top of `main()` (lines 144-169, from the env reads through the `Controller` literal) with:

```go
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
	}
```

And update the startup log line (was line 180):

```go
	log.Printf("controller up: %d games", len(c.games))
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd controller && go test ./...`
Expected: PASS (all of `pool`, `k8s`, `parse`, `velocity`, `server` tests including the two new ones).

- [ ] **Step 6: Vet**

Run: `cd controller && go vet ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add controller/main.go controller/k8s.go controller/server_test.go
git commit -m "feat(controller): config-driven multi-game pool + registration"
```

---

### Task 3: Parkour pure core — course generation + win check (`Course.java` + test)

**Files:**
- Create: `plugins/parkour-game/build.gradle`
- Create: `plugins/parkour-game/settings.gradle`
- Create: `plugins/parkour-game/src/main/java/mc/parkour/Course.java`
- Create: `plugins/parkour-game/src/main/java/mc/parkour/Done.java`
- Test: `plugins/parkour-game/src/test/java/mc/parkour/CourseTest.java`

**Interfaces:**
- Produces: `record Course.Vec3(int x, int y, int z)`; `static List<Course.Vec3> Course.generate(long seed, int length)`; `static boolean Course.atFinish(double px, double py, double pz, Vec3 finish)`; `static Vec3 Course.start()`; `static Vec3 Course.finish(List<Vec3> course)`; `static String Done.doneUrl(String base, String id)`.
- Consumes: nothing.

- [ ] **Step 1: Create the Gradle scaffolding**

```groovy
// plugins/parkour-game/build.gradle
plugins { id 'java' }
group = 'mc'
version = '0.1.0'
java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }
repositories {
  mavenCentral()
  maven { url = 'https://repo.papermc.io/repository/maven-public/' }
}
dependencies {
  compileOnly 'io.papermc.paper:paper-api:1.21.8-R0.1-SNAPSHOT'
  testImplementation 'org.junit.jupiter:junit-jupiter:5.10.2'
  testRuntimeOnly 'org.junit.platform:junit-platform-launcher'  // required since Gradle 9
}
test { useJUnitPlatform() }
jar { archiveFileName = 'parkour-plugin.jar' }
```

```groovy
// plugins/parkour-game/settings.gradle
rootProject.name = 'parkour-game'
```

- [ ] **Step 2: Write the failing test**

```java
// plugins/parkour-game/src/test/java/mc/parkour/CourseTest.java
package mc.parkour;

import org.junit.jupiter.api.Test;
import java.util.List;
import static org.junit.jupiter.api.Assertions.*;

class CourseTest {
  @Test void deterministicForSameSeed() {
    assertEquals(Course.generate(42L, 10), Course.generate(42L, 10));
  }

  @Test void lengthIsPlatformCountPlusStart() {
    assertEquals(11, Course.generate(1L, 10).size()); // start + 10 steps
  }

  @Test void everyGapIsJumpable() {
    // Consecutive platforms must be reachable: horizontal step <= 4, |dy| <= 1.
    for (long seed = 0; seed < 50; seed++) {
      List<Course.Vec3> c = Course.generate(seed, 30);
      for (int i = 1; i < c.size(); i++) {
        Course.Vec3 a = c.get(i - 1), b = c.get(i);
        int dx = Math.abs(b.x() - a.x()), dz = Math.abs(b.z() - a.z());
        assertTrue(dx + dz >= 1 && dx + dz <= 4, "seed " + seed + " step " + i + " gap " + (dx + dz));
        assertTrue(Math.abs(b.y() - a.y()) <= 1, "seed " + seed + " step " + i + " dy");
      }
    }
  }

  @Test void atFinishWithinThreshold() {
    Course.Vec3 f = new Course.Vec3(10, 64, 3);
    assertTrue(Course.atFinish(10.4, 64.0, 3.2, f));
    assertFalse(Course.atFinish(7.0, 64.0, 3.0, f));
  }

  @Test void doneUrlTrimsSlash() {
    assertEquals("http://c:8080/instances/x/done", Done.doneUrl("http://c:8080/", "x"));
  }
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v "$PWD/plugins/parkour-game":/work -w /work gradle:jdk21 gradle test`
Expected: FAIL — `Course`/`Done` do not exist (compile error).

- [ ] **Step 4: Write the implementation**

```java
// plugins/parkour-game/src/main/java/mc/parkour/Course.java
package mc.parkour;

import java.util.ArrayList;
import java.util.List;
import java.util.Random;

/** Pure, Bukkit-free course generator + win check (mirrors the stub's Done split). */
public final class Course {
  private Course() {}

  public record Vec3(int x, int y, int z) {}

  private static final int START_Y = 64;

  /** Start platform — fixed so the plugin can place spawn before generating. */
  public static Vec3 start() { return new Vec3(0, START_Y, 0); }

  /**
   * Deterministic course of `length` jumps from the start. Each step advances
   * +x by a jumpable gap (2-3) with a small sideways (0-1) and vertical (-1..1)
   * offset, so every gap satisfies horizontalStep <= 4 and |dy| <= 1.
   */
  public static List<Vec3> generate(long seed, int length) {
    Random rnd = new Random(seed);
    List<Vec3> out = new ArrayList<>(length + 1);
    Vec3 cur = start();
    out.add(cur);
    for (int i = 0; i < length; i++) {
      int dx = 2 + rnd.nextInt(2);          // 2..3 forward
      int dz = rnd.nextInt(2);              // 0..1 sideways  (dx+dz <= 4)
      if (rnd.nextBoolean()) dz = -dz;
      int dy = rnd.nextInt(3) - 1;          // -1..1
      cur = new Vec3(cur.x() + dx, cur.y() + dy, cur.z() + dz);
      out.add(cur);
    }
    return out;
  }

  public static Vec3 finish(List<Vec3> course) { return course.get(course.size() - 1); }

  /** True when the player is within ~1.5 blocks (horizontally + vertically) of finish. */
  public static boolean atFinish(double px, double py, double pz, Vec3 finish) {
    return Math.abs(px - finish.x()) <= 1.5
        && Math.abs(pz - finish.z()) <= 1.5
        && Math.abs(py - finish.y()) <= 2.0;
  }
}
```

```java
// plugins/parkour-game/src/main/java/mc/parkour/Done.java
package mc.parkour;

/** Pure helper, unit-testable without Bukkit (shared contract with the stub game). */
public final class Done {
  private Done() {}

  /** POST target on the controller; it then unregisters + deletes this pod. */
  public static String doneUrl(String base, String instanceId) {
    if (base.endsWith("/")) base = base.substring(0, base.length() - 1);
    return base + "/instances/" + instanceId + "/done";
  }
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v "$PWD/plugins/parkour-game":/work -w /work gradle:jdk21 gradle test`
Expected: BUILD SUCCESSFUL, 5 tests passed.

- [ ] **Step 6: Commit**

```bash
git add plugins/parkour-game/build.gradle plugins/parkour-game/settings.gradle \
  plugins/parkour-game/src/main/java/mc/parkour/Course.java \
  plugins/parkour-game/src/main/java/mc/parkour/Done.java \
  plugins/parkour-game/src/test/java/mc/parkour/CourseTest.java
git commit -m "feat(parkour): pure course generator + win check"
```

---

### Task 4: Parkour Bukkit plugin (`ParkourPlugin.java` + `plugin.yml`)

The framework-bound glue: create a void world, place the generated course, teleport joiners, fire `/done` on finish, reset on falling. No unit test (Bukkit-bound) — correctness of the logic lives in Task 3's tests.

**Files:**
- Create: `plugins/parkour-game/src/main/java/mc/parkour/ParkourPlugin.java`
- Create: `plugins/parkour-game/src/main/resources/plugin.yml`

**Interfaces:**
- Consumes: `Course.generate/start/finish/atFinish`, `Course.Vec3`, `Done.doneUrl` (Task 3).
- Produces: a runnable Paper plugin `mc.parkour.ParkourPlugin`.

- [ ] **Step 1: Write the plugin**

```java
// plugins/parkour-game/src/main/java/mc/parkour/ParkourPlugin.java
package mc.parkour;

import org.bukkit.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.PlayerJoinEvent;
import org.bukkit.event.player.PlayerMoveEvent;
import org.bukkit.generator.ChunkGenerator;
import org.bukkit.plugin.java.JavaPlugin;
import java.net.URI;
import java.net.http.*;
import java.util.List;

public final class ParkourPlugin extends JavaPlugin implements Listener {
  private World world;
  private Location startLoc;
  private Course.Vec3 finish;
  private double floorY;
  private volatile boolean finished = false;

  @Override public void onEnable() {
    // ponytail: seed the course from INSTANCE_ID so a pod's course is stable across reloads.
    long seed = String.valueOf(System.getenv("INSTANCE_ID")).hashCode();
    List<Course.Vec3> course = Course.generate(seed, 20);
    finish = Course.finish(course);

    // Runtime-generated void world — no baked .slime (the convention allows either).
    world = new WorldCreator("parkour")
        .generator(new ChunkGenerator() {})   // empty generator => void
        .generateStructures(false)
        .createWorld();

    Material block = Material.QUARTZ_BLOCK;
    for (Course.Vec3 v : course) world.getBlockAt(v.x(), v.y(), v.z()).setType(block);
    world.getBlockAt(finish.x(), finish.y(), finish.z()).setType(Material.EMERALD_BLOCK);

    Course.Vec3 s = Course.start();
    startLoc = new Location(world, s.x() + 0.5, s.y() + 1, s.z() + 0.5);
    floorY = s.y() - 10.0;
    world.setSpawnLocation(startLoc);

    getServer().getPluginManager().registerEvents(this, this);
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    p.teleport(startLoc);
    p.sendMessage(ChatColor.AQUA + "Parkour! Reach the green block to win.");
  }

  @EventHandler public void onMove(PlayerMoveEvent e) {
    if (finished) return;
    Location l = e.getTo();
    if (l.getY() < floorY) { e.getPlayer().teleport(startLoc); return; }
    if (Course.atFinish(l.getX(), l.getY(), l.getZ(), finish)) win(e.getPlayer());
  }

  private void win(Player p) {
    finished = true;
    p.sendMessage(ChatColor.GREEN + "You win! Recycling...");
    String url = Done.doneUrl(System.getenv("CONTROLLER_URL"), System.getenv("INSTANCE_ID"));
    getServer().getScheduler().runTaskAsynchronously(this, () -> postDone(url));
  }

  private void postDone(String url) {
    try {
      HttpClient.newHttpClient().send(
          HttpRequest.newBuilder(URI.create(url)).POST(HttpRequest.BodyPublishers.noBody()).build(),
          HttpResponse.BodyHandlers.discarding());
    } catch (Exception ex) {
      getLogger().warning("POST done failed: " + ex.getMessage());
    }
  }
}
```

- [ ] **Step 2: Write `plugin.yml`**

```yaml
# plugins/parkour-game/src/main/resources/plugin.yml
name: ParkourGame
version: 0.1.0
main: mc.parkour.ParkourPlugin
api-version: '1.21'
```

- [ ] **Step 3: Build the jar to verify it compiles**

Run: `docker run --rm -u $(id -u):$(id -g) -e GRADLE_USER_HOME=/work/.gradle -v "$PWD/plugins/parkour-game":/work -w /work gradle:jdk21 gradle build`
Expected: BUILD SUCCESSFUL; `plugins/parkour-game/build/libs/parkour-plugin.jar` exists.

- [ ] **Step 4: Commit**

```bash
git add plugins/parkour-game/src/main/java/mc/parkour/ParkourPlugin.java \
  plugins/parkour-game/src/main/resources/plugin.yml
git commit -m "feat(parkour): void-world plugin generates course + auto /done on win"
```

---

### Task 5: Wire it together — image, Make, ConfigMap, lobby entry, convention doc

Glue + deploy + the live end-to-end gate. Each piece is small; together they make parkour reachable from the compass.

**Files:**
- Create: `images/minigame-parkour/Dockerfile`
- Modify: `Makefile` (add `PARKOUR_GRADLE`, `build-parkour`; wire into `load`)
- Modify: `deploy/k8s/controller.yaml` (add `minigames` ConfigMap; mount `/config`; drop `GAME`/`POOL_SIZE`/`MINIGAME_IMAGE` env)
- Modify: `plugins/lobby-plugin/src/main/resources/config.yml` (add parkour entry)
- Create: `docs/minigame-convention.md`

**Interfaces:**
- Consumes: `mc/minigame-parkour:dev` image (Task 4 jar); controller reading `/config/games.json` (Task 2).
- Produces: a running 2-game platform.

- [ ] **Step 1: Parkour image (no baked world)**

```dockerfile
# images/minigame-parkour/Dockerfile
FROM mc/mc-base:dev
# Parkour generates its void world + course at runtime — no baked .slime, no worlds.yml.
COPY parkour-plugin.jar /server/plugins/parkour-plugin.jar
```

- [ ] **Step 2: Makefile — build target + load wiring**

After the `build-minigame-stub` block (around line 70), add:

```makefile
PARKOUR_GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/parkour-game:/work -w /work gradle:jdk21 gradle

build-parkour: build-base
	$(PARKOUR_GRADLE) build
	cp plugins/parkour-game/build/libs/parkour-plugin.jar images/minigame-parkour/parkour-plugin.jar
	docker build -t mc/minigame-parkour:dev images/minigame-parkour
	rm -f images/minigame-parkour/parkour-plugin.jar
```

Add `build-parkour` to the `.PHONY` line (line 12) and to the `load` target: change the `load:` dependency line and add a `kind load` line.

`load:` recipe becomes:

```makefile
load: build-velocity build-lobby build-controller build-minigame-stub build-parkour
	kind load docker-image mc/velocity:dev --name $(CLUSTER)
	kind load docker-image mc/lobby:dev --name $(CLUSTER)
	kind load docker-image mc/controller:dev --name $(CLUSTER)
	kind load docker-image mc/minigame-stub:dev --name $(CLUSTER)
	kind load docker-image mc/minigame-parkour:dev --name $(CLUSTER)
```

- [ ] **Step 3: controller.yaml — ConfigMap + mount, drop single-game env**

Add this ConfigMap at the top of `deploy/k8s/controller.yaml` (before the ServiceAccount, with a trailing `---`):

```yaml
apiVersion: v1
kind: ConfigMap
metadata: { name: minigames, namespace: mc }
data:
  games.json: |
    [{"name":"stub","image":"mc/minigame-stub:dev","poolSize":1},
     {"name":"parkour","image":"mc/minigame-parkour:dev","poolSize":1}]
---
```

In the Deployment, replace the `env:` list (lines 35-40) with just the two URLs:

```yaml
          env:
            - { name: VELOCITY_REGISTER_URL, value: http://velocity.mc.svc.cluster.local:8080 }
            - { name: CONTROLLER_URL, value: http://controller.mc.svc.cluster.local:8080 }
```

Add a `config` volumeMount alongside the existing `token` mount:

```yaml
          volumeMounts:
            - { name: token, mountPath: /secret, readOnly: true }
            - { name: config, mountPath: /config, readOnly: true }
```

Add the `config` volume alongside the existing `token` volume:

```yaml
      volumes:
        - name: token
          secret:
            secretName: controller-token
            items: [ { key: controller.token, path: controller.token } ]
        - name: config
          configMap:
            name: minigames
```

- [ ] **Step 4: Lobby compass — add the parkour entry**

Replace `plugins/lobby-plugin/src/main/resources/config.yml` with:

```yaml
# Slice 2: two games. onClick maps target -> controller /allocate.
controller: http://controller.mc.svc.cluster.local:8080
minigames:
  - { name: Stub Game, material: GRASS_BLOCK, target: stub }
  - { name: Parkour, material: QUARTZ_BLOCK, target: parkour }
```

- [ ] **Step 5: Convention doc**

```markdown
# Minigame image convention

A minigame is a disposable Paper backend the controller spawns, registers with
Velocity, and deletes when the match ends. To add one:

## 1. Image
`FROM mc/mc-base:dev` and bake your plugin jar into `/server/plugins/`. mc-base
already provides ASP, the forwarding secret mount, `online-mode=false`, and SWM.

## 2. World
Provide a world the plugin teleports joiners into — either:
- **baked** a `.slime` world + `worlds.yml` under
  `/opt/config/plugins/SlimeWorldManager/` (see `images/minigame-stub`), or
- **generated** at runtime via `WorldCreator` (see `images/minigame-parkour`,
  which uses an empty `ChunkGenerator` for a void world).

## 3. Game-over contract
When the match ends, POST `{CONTROLLER_URL}/instances/{INSTANCE_ID}/done`. Both
env vars are injected by the controller. The controller then unregisters and
deletes the pod, and reconcile spawns a replacement. Trigger it however the game
ends — an op command (stub's `/endgame`) or a win condition (parkour's finish).

## 4. Testable core
Keep game logic (course generation, win checks, URL building) in **Bukkit-free**
classes so it unit-tests without paper-api on the test classpath — see
`mc.parkour.Course` / `mc.parkour.Done` / `mc.stub.Done`.

## 5. Register the game (two places)
- `deploy/k8s/controller.yaml` → the `minigames` ConfigMap: `{name, image, poolSize}`.
- `plugins/lobby-plugin/src/main/resources/config.yml` → a compass entry with
  `target: <name>`.

The `name` in the ConfigMap and the compass `target` must match the `game` label
the controller sets on pods.
```

- [ ] **Step 6: Build everything, load, apply**

Run:
```bash
make build-parkour
kind load docker-image mc/minigame-parkour:dev mc/controller:dev mc/lobby:dev --name mc
kubectl apply -f deploy/k8s/controller.yaml
kubectl rollout restart deploy/controller deploy/lobby -n mc
kubectl rollout status deploy/controller -n mc --timeout=120s
```
Expected: controller + lobby roll out cleanly.

- [ ] **Step 7: Verify the pools and registration**

Run:
```bash
sleep 30
kubectl get pods -n mc -l app=minigame -L game
kubectl logs -n mc deploy/controller --tail=20
```
Expected: at least one `mg-stub-*` and one `mg-parkour-*` pod; controller log shows `controller up: 2 games` and no create/register errors.

- [ ] **Step 8: Allocate-by-game smoke (in-cluster)**

Run:
```bash
TOKEN=$(kubectl get secret controller-token -n mc -o jsonpath='{.data.controller\.token}' | base64 -d)
kubectl run curl-test -n mc --rm -i --restart=Never --image=curlimages/curl -- \
  -s -XPOST http://controller.mc.svc.cluster.local:8080/allocate \
  -d '{"game":"parkour"}'
```
Expected: JSON `{"server":"mg-parkour-...","address":"<ip>:25565"}`. (An unknown game like `{"game":"ghost"}` returns 404.)

- [ ] **Step 9: Commit**

```bash
git add images/minigame-parkour/Dockerfile Makefile deploy/k8s/controller.yaml \
  plugins/lobby-plugin/src/main/resources/config.yml docs/minigame-convention.md
git commit -m "feat(slice-2): parkour image, minigames ConfigMap, lobby entry, convention doc"
```

---

### Task 6: Manual acceptance + handoff doc

**Files:**
- Modify: `docs/superpowers/SLICE-2-HANDOFF.md` (mark done) or create `docs/superpowers/SLICE-3-HANDOFF.md`
- Modify: `AGENTS.md` if it tracks slice status

- [ ] **Step 1: Manual end-to-end (human, one Minecraft 1.21.8 client)**

Connect to the Velocity LoadBalancer IP (`kubectl -n mc get svc velocity -o jsonpath='{.status.loadBalancer.ingress[0].ip}'`), open the compass, confirm **two** entries (Stub Game, Parkour). Click Parkour → you spawn on the quartz start platform → jump to the emerald finish → you should see "You win! Recycling..." and the pod recycles (a fresh `mg-parkour-*` appears). Click Stub Game → still works.

- [ ] **Step 2: Write the Slice 3 handoff**

Record: Slice 2 delivered config-driven multi-game + procedural parkour + convention doc. Carryover: games live in the `minigames` ConfigMap; adding a game = new image (per `docs/minigame-convention.md`) + ConfigMap entry + lobby entry. Open Slice 3 question: the scaffolding generator (now that two games exist by hand) and/or richer lifecycle.

- [ ] **Step 3: Commit**

```bash
git add docs/ AGENTS.md
git commit -m "docs(slice-2): mark done, add Slice 3 handoff"
```

---

## Self-Review

**Spec coverage:**
- §1 Games config (ConfigMap JSON) → Task 1 (parse) + Task 5 Step 3 (ConfigMap + mount). ✓
- §2 Controller refactor (struct, createPod sig, reconcile loop, allocate 404/503, handleDone listPods("")) → Task 2. ✓
- §3 Parkour procedural (void world, generate, win→/done, fall→reset, pure core) → Task 3 (pure) + Task 4 (Bukkit). ✓
- §4 Lobby compass entry (config only) → Task 5 Step 4. ✓
- §5 Convention doc → Task 5 Step 5. ✓
- §6 Makefile + deploy → Task 5 Steps 1-3. ✓
- Runnable checks → Task 1/2 Go tests, Task 3 Java tests, Task 5 Steps 6-8 + Task 6 manual. ✓
- Deferred (generator, dynamic discovery, live reload) → none implemented; noted in Task 6 handoff. ✓

**Placeholder scan:** No TBD/TODO; every code step shows full code; no "add error handling" hand-waves. ✓

**Type consistency:** `Game{Name,Image string; PoolSize int}` used identically in Tasks 1/2/5. `createPod(name, game, image string) error` consistent across the test helper, struct, reconcile call, and `main` closure. `Course.Vec3(int x,int y,int z)` + `generate(long,int)` + `atFinish(double,double,double,Vec3)` consistent across Task 3 test, Task 3 impl, Task 4 use. `Done.doneUrl(String,String)` consistent. `listPods("")`=all consistent between k8s.go change and handleDone. ✓
