# Slice 0 (Skeleton) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A player connects through a custom Velocity proxy into a custom Advanced Slime Paper lobby on a local `kind` cluster with a real LoadBalancer IP.

**Architecture:** Three custom images (`velocity`, `mc-base`, `lobby` built FROM `mc-base`) plus one Paper plugin. `cloud-provider-kind` gives a `Service type=LoadBalancer` a host-reachable IP; that routes TCP 25565 → Velocity → (modern forwarding) → lobby. Lobby world is a baked `.slime` loaded into memory via SWM's file data source. No controller, no minigame pods (Slice 1).

**Tech Stack:** kind, cloud-provider-kind, kubectl, Docker, eclipse-temurin JRE 21, Advanced Slime Paper (ASP), Velocity 3.x, Java 21 + Gradle (lobby plugin), Go stdlib (smoke ping), Make.

## Global Constraints

- **No itzg images.** Every image is built from scratch in this repo.
- **Registry-free local loop:** `docker build` → `kind load docker-image` → `kubectl apply`. Never push to a registry.
- **Java 21** (`eclipse-temurin:21-jre` runtime base) everywhere on the JVM.
- **Minecraft Java = TCP/25565 only.** No UDP in this slice.
- **Namespace:** all cluster resources live in namespace `mc`.
- **Forwarding secret is never baked into an image.** It lives in a k8s `Secret` (name `velocity-forwarding`, key `forwarding.secret`) mounted at `/secret/forwarding.secret` in both `velocity` and `lobby`. Entrypoints fail fast (exit non-zero) if it is missing.
- **Stateless pods:** slime worlds load into memory; nothing persists back to disk. Server working dir is an `emptyDir`.
- **Mark every deliberate deferral** with a `ponytail:` comment naming what's skipped and when to add it.
- Pin exact upstream versions in a single `images/versions.env` and source it from Dockerfiles/Makefile — no version literal appears twice.

---

## File Structure

```
mc-platform/
  images/
    versions.env          # ASP_URL, VELOCITY_URL, JRE_TAG — single source of versions
    velocity/
      Dockerfile
      velocity.toml        # modern forwarding, static lobby server
      entrypoint.sh        # fail-fast on missing secret, then exec velocity
    mc-base/
      Dockerfile
      entrypoint.sh        # fail-fast on secret; write paper velocity-forwarding + SWM config; exec server
      config/              # paper-global.yml fragment + SWM data-source config (reconciled to pinned ASP)
    lobby/
      Dockerfile           # FROM mc-base; add plugin jar + lobby.slime; set default world
      config.yml           # lobby-plugin config: static minigame list
  plugins/
    lobby-plugin/
      build.gradle
      settings.gradle
      src/main/java/mc/lobby/LobbyPlugin.java       # join listener + compass
      src/main/java/mc/lobby/Menu.java              # pure config→entries logic (unit-tested)
      src/main/resources/plugin.yml
      src/test/java/mc/lobby/MenuTest.java
  tools/
    smoke/
      main.go              # stdlib server-list ping
      main_test.go         # varint encode test
  deploy/
    kind/cluster.yaml
    k8s/
      namespace.yaml
      velocity.yaml        # Deployment + Service type=LoadBalancer
      lobby.yaml           # Deployment + Service ClusterIP
  worlds/
    lobby.slime            # produced by `make lobby-world` (git-tracked asset)
  Makefile
  README.md
```

---

## Task 1: Cluster bring-up (kind + cloud-provider-kind + LB proof)

**Files:**
- Create: `deploy/kind/cluster.yaml`, `deploy/k8s/namespace.yaml`, `Makefile`, `images/versions.env`, `README.md`

**Interfaces:**
- Produces: `make cluster` (creates kind cluster `mc` + starts cloud-provider-kind), `make down` (deletes cluster + stops cloud-provider-kind). Namespace `mc` exists after `make cluster`.

- [ ] **Step 1: Pin versions**

`images/versions.env`:
```sh
JRE_TAG=21-jre
VELOCITY_URL=https://api.papermc.io/v2/projects/velocity/versions/3.4.0-SNAPSHOT/builds/<BUILD>/downloads/velocity-3.4.0-SNAPSHOT-<BUILD>.jar
# ASP: pick the latest Advanced Slime Paper release jar URL for MC 1.21.x from
# https://github.com/InfernalSuite/AdvancedSlimePaper/releases and pin it here.
ASP_URL=<pinned-asp-jar-url>
```
Resolve `<BUILD>` / `<pinned-asp-jar-url>` to concrete URLs now; no placeholders left in the committed file.

- [ ] **Step 2: kind cluster config**

`deploy/kind/cluster.yaml`:
```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: mc
nodes:
  - role: control-plane
```
No `extraPortMappings` — `cloud-provider-kind` provides the LB IP. (`ponytail: single-node cluster; add workers when a real workload needs scheduling spread`.)

- [ ] **Step 3: namespace**

`deploy/k8s/namespace.yaml`:
```yaml
apiVersion: v1
kind: Namespace
metadata: { name: mc }
```

- [ ] **Step 4: Makefile cluster targets**

```makefile
CLUSTER := mc
.PHONY: cluster down
cluster:
	kind create cluster --config deploy/kind/cluster.yaml
	kubectl apply -f deploy/k8s/namespace.yaml
	# cloud-provider-kind must run alongside kind to assign LoadBalancer IPs.
	# Install once: go install sigs.k8s.io/cloud-provider-kind@latest
	cloud-provider-kind > /tmp/cpkind.log 2>&1 & echo $$! > /tmp/cpkind.pid
	@echo "cluster up; cloud-provider-kind pid $$(cat /tmp/cpkind.pid)"
down:
	-kill $$(cat /tmp/cpkind.pid) 2>/dev/null
	kind delete cluster --name $(CLUSTER)
```
(`ponytail: background pid in /tmp is fine for one local cluster; supervise it properly only if this ever runs unattended`.)

- [ ] **Step 5: Verify LB assignment**

Run:
```bash
make cluster
kubectl create deploy echo --image=registry.k8s.io/e2e-test-images/agnhost:2.39 -n mc -- /agnhost netexec --http-port=8080
kubectl expose deploy echo -n mc --type=LoadBalancer --port=8080
kubectl get svc echo -n mc -w
```
Expected: `EXTERNAL-IP` transitions from `<pending>` to a real IP within ~30s. Then `curl http://<EXTERNAL-IP>:8080/` returns agnhost output.

- [ ] **Step 6: Clean the proof + commit**

```bash
kubectl delete deploy/echo svc/echo -n mc
git add -A && git commit -m "feat(deploy): kind cluster + cloud-provider-kind LB bring-up"
```

---

## Task 2: `velocity` image

**Files:**
- Create: `images/velocity/Dockerfile`, `images/velocity/velocity.toml`, `images/velocity/entrypoint.sh`
- Modify: `Makefile` (add `build-velocity`)

**Interfaces:**
- Consumes: `images/versions.env` (`VELOCITY_URL`, `JRE_TAG`).
- Produces: image `mc/velocity:dev`. Listens 25565, modern forwarding, reads secret at `/secret/forwarding.secret`, routes default to server `lobby` at `lobby.mc.svc.cluster.local:25565`.

- [ ] **Step 1: velocity.toml**

`images/velocity/velocity.toml`:
```toml
config-version = "2.7"
bind = "0.0.0.0:25565"
motd = "<#09add3>mc-platform lobby"
online-mode = true
player-info-forwarding-mode = "modern"
forwarding-secret-file = "/secret/forwarding.secret"
[servers]
lobby = "lobby.mc.svc.cluster.local:25565"
try = ["lobby"]
[advanced]
haproxy-protocol = false
```

- [ ] **Step 2: entrypoint (fail-fast on missing secret)**

`images/velocity/entrypoint.sh`:
```sh
#!/bin/sh
set -e
[ -s /secret/forwarding.secret ] || { echo "FATAL: /secret/forwarding.secret missing/empty" >&2; exit 1; }
exec java -Xms256M -Xmx512M -jar /app/velocity.jar
```

- [ ] **Step 3: Dockerfile**

`images/velocity/Dockerfile`:
```dockerfile
ARG JRE_TAG=21-jre
FROM eclipse-temurin:${JRE_TAG}
ARG VELOCITY_URL
WORKDIR /app
ADD ${VELOCITY_URL} /app/velocity.jar
COPY velocity.toml /app/velocity.toml
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && useradd -r -u 1000 mc && chown -R mc /app
USER mc
EXPOSE 25565
ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 4: Makefile target**

```makefile
include images/versions.env
export
.PHONY: build-velocity
build-velocity:
	docker build -t mc/velocity:dev \
	  --build-arg JRE_TAG=$(JRE_TAG) --build-arg VELOCITY_URL=$(VELOCITY_URL) \
	  images/velocity
```

- [ ] **Step 5: Verify build + fail-fast + proxy answers**

Run:
```bash
make build-velocity
# fail-fast path (no secret) must exit non-zero:
docker run --rm mc/velocity:dev; echo "exit=$?"   # expect FATAL + exit=1
# happy path: provide a secret, proxy should boot and answer a ping
mkdir -p /tmp/sec && echo testsecret > /tmp/sec/forwarding.secret
docker run --rm -d --name vtest -p 25565:25565 -v /tmp/sec:/secret:ro mc/velocity:dev
sleep 8 && (echo > /dev/tcp/127.0.0.1/25565) && echo "port open"
docker logs vtest | grep -i "Listening on"
docker rm -f vtest
```
Expected: fail-fast prints FATAL and `exit=1`; happy path logs `Listening on /0.0.0.0:25565` and the port is open.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(velocity): custom Velocity image with modern forwarding + secret fail-fast"
```

---

## Task 3: `mc-base` image

**Files:**
- Create: `images/mc-base/Dockerfile`, `images/mc-base/entrypoint.sh`, `images/mc-base/config/` (paper velocity-forwarding fragment + SWM file data-source config)
- Modify: `Makefile` (add `build-base`)

**Interfaces:**
- Consumes: `images/versions.env` (`ASP_URL`, `JRE_TAG`).
- Produces: image `mc/mc-base:dev`. Boots ASP with Velocity modern forwarding ON and SWM **file** data source rooted at `/worlds`. Fails fast if secret missing. Not run standalone in production (no baked world) — `lobby`/minigame images add the world.

- [ ] **Step 1: Read the pinned ASP docs, reconcile config**

Download the pinned `ASP_URL` jar locally and read its bundled SlimeWorldManager config docs/sample (ASP ships SWM; the exact file name + schema is version-specific). Confirm: (a) where the file data source directory is configured, (b) how a world is declared to load at startup from that data source. Write the two config files in `images/mc-base/config/` to match the pinned build. Do not invent fields the pinned build doesn't have.

- [ ] **Step 2: paper velocity-forwarding fragment**

`images/mc-base/config/paper-global.yml` (only the proxies block matters here; entrypoint merges/copies it):
```yaml
proxies:
  velocity:
    enabled: true
    online-mode: true
    secret: ""   # filled at runtime from /secret/forwarding.secret by entrypoint
```

- [ ] **Step 3: entrypoint (fail-fast + inject secret + SWM file source)**

`images/mc-base/entrypoint.sh`:
```sh
#!/bin/sh
set -e
[ -s /secret/forwarding.secret ] || { echo "FATAL: /secret/forwarding.secret missing/empty" >&2; exit 1; }
SECRET=$(cat /secret/forwarding.secret)
mkdir -p /server/config /worlds
cp /opt/config/paper-global.yml /server/config/paper-global.yml
# inject secret without printing it
sed -i "s|secret: \"\"|secret: \"${SECRET}\"|" /server/config/paper-global.yml
# SWM data source + world-load config copied from /opt/config (reconciled in Step 1)
cp -r /opt/config/swm/. /server/ 2>/dev/null || true
cd /server
# Aikar flags: https://docs.papermc.io/paper/aikar-flags
exec java -Xms1G -Xmx2G \
  -XX:+UseG1GC -XX:+ParallelRefProcEnabled -XX:MaxGCPauseMillis=200 \
  -XX:+UnlockExperimentalVMOptions -XX:+DisableExplicitGC -XX:+AlwaysPreTouch \
  -XX:G1HeapRegionSize=8M -XX:G1NewSizePercent=30 -XX:G1MaxNewSizePercent=40 \
  -XX:G1ReservePercent=20 -XX:InitiatingHeapOccupancyPercent=15 \
  -jar /opt/server.jar nogui
```
(`ponytail: 1–2G heap hardcoded; make it an env knob only when a minigame actually needs a different size`.)

- [ ] **Step 4: Dockerfile**

`images/mc-base/Dockerfile`:
```dockerfile
ARG JRE_TAG=21-jre
FROM eclipse-temurin:${JRE_TAG}
ARG ASP_URL
RUN useradd -r -u 1000 mc && mkdir -p /server /worlds /opt/config && chown -R mc /server /worlds
ADD ${ASP_URL} /opt/server.jar
COPY config/ /opt/config/
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh && echo "eula=true" > /server/eula.txt && chown mc /server/eula.txt
USER mc
WORKDIR /server
EXPOSE 25565
ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 5: Verify build + security fail-fast (the non-trivial path)**

Run:
```bash
make build-base
docker run --rm mc/mc-base:dev; echo "exit=$?"   # expect FATAL + exit=1
```
Expected: missing-secret path prints FATAL and `exit=1`. (Full boot is exercised in Task 5 via the `lobby` image, which supplies a world.)

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(mc-base): ASP base image with SWM file source, velocity forwarding, secret fail-fast"
```

---

## Task 4: Lobby slime world asset (`make lobby-world`)

**Files:**
- Create: `worlds/lobby.slime` (generated, git-tracked), Makefile target `lobby-world`

**Interfaces:**
- Consumes: `mc/mc-base:dev` (Task 3).
- Produces: `worlds/lobby.slime` — a small superflat world in SWM file format, baked by Task 5.

- [ ] **Step 1: Generation target**

Add to `Makefile`:
```makefile
.PHONY: lobby-world
lobby-world:
	# Boot a throwaway mc-base, have SWM create an empty 'lobby' world in the
	# file data source, then copy the .slime out. The exact console command
	# (e.g. `swm create lobby ...`) is taken from the pinned ASP/SWM docs (Task 3, Step 1).
	./images/mc-base/make-lobby-world.sh
```
Write `images/mc-base/make-lobby-world.sh` to: run the container with a dummy secret, send the SWM "create world in file data source" console command, wait until the `.slime` appears under the file-source dir, `docker cp` it to `worlds/lobby.slime`, then remove the container. Reconcile the exact command/paths against the pinned build.

- [ ] **Step 2: Generate + sanity-check**

Run:
```bash
echo testsecret > /tmp/sec/forwarding.secret
make lobby-world
test -s worlds/lobby.slime && echo "slime created: $(wc -c < worlds/lobby.slime) bytes"
```
Expected: `worlds/lobby.slime` exists and is non-empty.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "feat(worlds): generate baked lobby.slime via SWM file data source"
```

---

## Task 5: `lobby-plugin` (Paper, Java + Gradle)

**Files:**
- Create: `plugins/lobby-plugin/{build.gradle,settings.gradle}`, `src/main/java/mc/lobby/LobbyPlugin.java`, `src/main/java/mc/lobby/Menu.java`, `src/main/resources/plugin.yml`, `src/test/java/mc/lobby/MenuTest.java`

**Interfaces:**
- Produces: `plugins/lobby-plugin/build/libs/lobby-plugin.jar`. `Menu.parse(List<Map<String,Object>>)` → `List<Menu.Entry>` where `Entry(String name, String material, String target)`.

- [ ] **Step 1: Write the failing test**

`src/test/java/mc/lobby/MenuTest.java`:
```java
package mc.lobby;
import org.junit.jupiter.api.Test;
import java.util.*;
import static org.junit.jupiter.api.Assertions.*;

class MenuTest {
  @Test void parsesEntriesPreservingOrder() {
    var raw = List.<Map<String,Object>>of(
      Map.of("name","BedWars","material","RED_BED","target","bedwars"),
      Map.of("name","SkyWars","material","FEATHER","target","skywars"));
    var out = Menu.parse(raw);
    assertEquals(2, out.size());
    assertEquals("BedWars", out.get(0).name());
    assertEquals("FEATHER", out.get(1).material());
    assertEquals("skywars", out.get(1).target());
  }
  @Test void skipsEntriesMissingFields() {
    var raw = List.<Map<String,Object>>of(Map.of("name","Broken"));
    assertTrue(Menu.parse(raw).isEmpty());
  }
}
```

- [ ] **Step 2: settings + build files**

`settings.gradle`: `rootProject.name = 'lobby-plugin'`

`build.gradle`:
```groovy
plugins { id 'java' }
group = 'mc'; version = '0.1.0'
java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }
repositories {
  mavenCentral()
  maven { url 'https://repo.papermc.io/repository/maven-public/' }
}
dependencies {
  compileOnly 'io.papermc.paper:paper-api:1.21.4-R0.1-SNAPSHOT'   // match pinned ASP MC version
  testImplementation 'org.junit.jupiter:junit-jupiter:5.10.2'
}
test { useJUnitPlatform() }
jar { archiveFileName = 'lobby-plugin.jar' }
```
(`ponytail: plain jar, no shadow — Menu has no runtime deps; add shadowJar only if a library is introduced`.)

- [ ] **Step 3: Run test, verify it fails**

Run: `cd plugins/lobby-plugin && gradle test`
Expected: FAIL — `Menu` does not exist (compile error).

- [ ] **Step 4: Implement `Menu` (pure logic)**

`src/main/java/mc/lobby/Menu.java`:
```java
package mc.lobby;
import java.util.*;
public final class Menu {
  public record Entry(String name, String material, String target) {}
  public static List<Entry> parse(List<Map<String,Object>> raw) {
    var out = new ArrayList<Entry>();
    for (var m : raw) {
      Object n = m.get("name"), mat = m.get("material"), t = m.get("target");
      if (n == null || mat == null || t == null) continue; // skip incomplete
      out.add(new Entry(n.toString(), mat.toString(), t.toString()));
    }
    return out;
  }
}
```

- [ ] **Step 5: Run test, verify it passes**

Run: `gradle test`
Expected: PASS (both tests).

- [ ] **Step 6: plugin.yml + Bukkit glue**

`src/main/resources/plugin.yml`:
```yaml
name: LobbyPlugin
version: 0.1.0
main: mc.lobby.LobbyPlugin
api-version: '1.21'
```

`src/main/java/mc/lobby/LobbyPlugin.java`:
```java
package mc.lobby;
import org.bukkit.*;
import org.bukkit.entity.Player;
import org.bukkit.event.*;
import org.bukkit.event.player.*;
import org.bukkit.event.inventory.*;
import org.bukkit.inventory.*;
import org.bukkit.inventory.meta.ItemMeta;
import org.bukkit.plugin.java.JavaPlugin;
import java.util.*;

public final class LobbyPlugin extends JavaPlugin implements Listener {
  private static final String TITLE = ChatColor.AQUA + "Minigames";
  private List<Menu.Entry> entries = List.of();

  @Override public void onEnable() {
    saveDefaultConfig();
    @SuppressWarnings("unchecked")
    var raw = (List<Map<String,Object>>) (List<?>) getConfig().getMapList("minigames");
    entries = Menu.parse(raw);
    getServer().getPluginManager().registerEvents(this, this);
  }

  @EventHandler public void onJoin(PlayerJoinEvent e) {
    Player p = e.getPlayer();
    p.setInvulnerable(true);
    p.setAllowFlight(true);
    p.setFlying(true);
    ItemStack compass = new ItemStack(Material.COMPASS);
    ItemMeta meta = compass.getItemMeta();
    meta.setDisplayName(ChatColor.AQUA + "Minigames " + ChatColor.GRAY + "(right-click)");
    compass.setItemMeta(meta);
    p.getInventory().setItem(0, compass);
  }

  @EventHandler public void onUse(PlayerInteractEvent e) {
    if (e.getItem() == null || e.getItem().getType() != Material.COMPASS) return;
    if (!e.getAction().name().startsWith("RIGHT_CLICK")) return;
    e.setCancelled(true);
    int rows = Math.max(1, (entries.size() + 8) / 9);
    Inventory inv = Bukkit.createInventory(null, rows * 9, TITLE);
    for (Menu.Entry en : entries) {
      ItemStack it = new ItemStack(Material.matchMaterial(en.material()) == null
        ? Material.PAPER : Material.matchMaterial(en.material()));
      ItemMeta m = it.getItemMeta();
      m.setDisplayName(ChatColor.YELLOW + en.name());
      it.setItemMeta(m);
      inv.addItem(it);
    }
    e.getPlayer().openInventory(inv);
  }

  @EventHandler public void onClick(InventoryClickEvent e) {
    if (!TITLE.equals(e.getView().getTitle())) return;
    e.setCancelled(true);
    if (e.getCurrentItem() == null) return;
    // ponytail: stub — Slice 1 sends the player via Velocity plugin-messaging ("Connect").
    e.getWhoClicked().sendMessage(ChatColor.GRAY + "Minigame servers arrive in Slice 1.");
    e.getWhoClicked().closeInventory();
  }
}
```

- [ ] **Step 7: Default config + build the jar**

`src/main/resources/config.yml`:
```yaml
minigames:
  - { name: BedWars, material: RED_BED, target: bedwars }
  - { name: SkyWars, material: FEATHER, target: skywars }
```
Run: `gradle build`
Expected: BUILD SUCCESSFUL; `build/libs/lobby-plugin.jar` exists.

- [ ] **Step 8: Commit**

```bash
git add -A && git commit -m "feat(lobby-plugin): join effects + compass minigame GUI (stub click)"
```

---

## Task 6: `lobby` image

**Files:**
- Create: `images/lobby/Dockerfile`, `images/lobby/config.yml`
- Modify: `Makefile` (add `build-lobby`, depends on plugin jar + world)

**Interfaces:**
- Consumes: `mc/mc-base:dev`, `plugins/lobby-plugin/build/libs/lobby-plugin.jar`, `worlds/lobby.slime`.
- Produces: image `mc/lobby:dev` that boots the lobby slime world with the plugin loaded.

- [ ] **Step 1: Dockerfile**

`images/lobby/Dockerfile`:
```dockerfile
FROM mc/mc-base:dev
COPY lobby-plugin.jar /server/plugins/lobby-plugin.jar
COPY lobby.slime /worlds/lobby.slime
COPY config.yml /server/plugins/LobbyPlugin/config.yml
# Default world = the slime 'lobby'. SWM "world to load at startup" config lives in
# mc-base/config (Task 3); this image only ensures the .slime is present in /worlds.
```
(If the pinned ASP requires naming the startup world in a config file rather than by presence, set it here by copying an override into `/server/config` — reconcile with Task 3 Step 1.)

- [ ] **Step 2: config.yml** — same content as the plugin default (`images/lobby/config.yml`), so the baked image is self-contained.

- [ ] **Step 3: Makefile target**

```makefile
.PHONY: build-lobby
build-lobby: build-base
	gradle -p plugins/lobby-plugin build
	cp plugins/lobby-plugin/build/libs/lobby-plugin.jar images/lobby/lobby-plugin.jar
	cp worlds/lobby.slime images/lobby/lobby.slime
	docker build -t mc/lobby:dev images/lobby
	rm -f images/lobby/lobby-plugin.jar images/lobby/lobby.slime
```

- [ ] **Step 4: Verify the lobby boots its slime world standalone**

Run:
```bash
docker run --rm -d --name lobbytest -p 25566:25565 -v /tmp/sec:/secret:ro mc/lobby:dev
sleep 25 && docker logs lobbytest | grep -iE "Done \(|world .*lobby"
docker rm -f lobbytest
```
Expected: log shows the server reaching `Done (` and the `lobby` slime world loaded. (This is the first full ASP boot — exercises mc-base + SWM + plugin together.)

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(lobby): lobby image = mc-base + plugin + baked lobby.slime"
```

---

## Task 7: Smoke pinger (Go stdlib)

**Files:**
- Create: `tools/smoke/main.go`, `tools/smoke/main_test.go`

**Interfaces:**
- Produces: `go run ./tools/smoke <host> <port>` — exits 0 if the server-list ping succeeds, non-zero otherwise.

- [ ] **Step 1: Write the failing test**

`tools/smoke/main_test.go`:
```go
package main
import ("bytes"; "testing")
func TestVarInt(t *testing.T) {
	cases := map[int][]byte{0:{0x00}, 1:{0x01}, 127:{0x7f}, 128:{0x80,0x01}, 300:{0xac,0x02}}
	for in, want := range cases {
		var b bytes.Buffer
		writeVarInt(&b, in)
		if !bytes.Equal(b.Bytes(), want) {
			t.Errorf("varint(%d)=%x want %x", in, b.Bytes(), want)
		}
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

Run: `cd tools/smoke && go test ./...`
Expected: FAIL — `writeVarInt` undefined.

- [ ] **Step 3: Implement the pinger**

`tools/smoke/main.go`:
```go
// Minimal Minecraft server-list ping (status). stdlib only.
// ponytail: just enough protocol to confirm the proxy answers; swap for mcstatus if richer checks are ever needed.
package main

import (
	"bytes"; "encoding/binary"; "fmt"; "net"; "os"; "time"
)

func writeVarInt(b *bytes.Buffer, v int) {
	uv := uint32(v)
	for {
		if uv&^0x7f == 0 { b.WriteByte(byte(uv)); return }
		b.WriteByte(byte(uv&0x7f | 0x80)); uv >>= 7
	}
}
func packet(id byte, payload []byte) []byte {
	var body bytes.Buffer
	body.WriteByte(id); body.Write(payload)
	var out bytes.Buffer
	writeVarInt(&out, body.Len()); out.Write(body.Bytes())
	return out.Bytes()
}
func main() {
	if len(os.Args) != 3 { fmt.Println("usage: smoke <host> <port>"); os.Exit(2) }
	host, port := os.Args[1], os.Args[2]
	addr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil { fmt.Println("dial:", err); os.Exit(1) }
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	var hs bytes.Buffer
	writeVarInt(&hs, 766)                    // protocol version (any recent)
	writeVarInt(&hs, len(host)); hs.WriteString(host)
	binary.Write(&hs, binary.BigEndian, uint16(mustPort(port)))
	writeVarInt(&hs, 1)                       // next state = status
	conn.Write(packet(0x00, hs.Bytes()))      // handshake
	conn.Write(packet(0x00, nil))             // status request
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err != nil { fmt.Println("no status response:", err); os.Exit(1) }
	fmt.Println("OK: proxy answered status ping")
}
func mustPort(s string) int {
	var p int; fmt.Sscanf(s, "%d", &p); return p
}
```

- [ ] **Step 4: Run test, verify it passes**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(smoke): stdlib Minecraft status-ping smoke check"
```

---

## Task 8: k8s manifests + `make up` / `make smoke` integration

**Files:**
- Create: `deploy/k8s/velocity.yaml`, `deploy/k8s/lobby.yaml`
- Modify: `Makefile` (add `load`, `apply`, `up`, `smoke`)

**Interfaces:**
- Consumes: every image + manifest above.
- Produces: `make up` (cluster → build → load → secret → apply), `make smoke` (ping the LB IP).

- [ ] **Step 1: lobby manifest**

`deploy/k8s/lobby.yaml`:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: lobby, namespace: mc }
spec:
  replicas: 1
  selector: { matchLabels: { app: lobby } }
  template:
    metadata: { labels: { app: lobby } }
    spec:
      containers:
        - name: lobby
          image: mc/lobby:dev
          imagePullPolicy: IfNotPresent
          ports: [ { containerPort: 25565 } ]
          volumeMounts:
            - { name: secret, mountPath: /secret, readOnly: true }
            - { name: work, mountPath: /server/cache }   # ephemeral scratch
          readinessProbe:
            tcpSocket: { port: 25565 }
            initialDelaySeconds: 20
            periodSeconds: 5
      volumes:
        - name: secret
          secret: { secretName: velocity-forwarding, items: [ { key: forwarding.secret, path: forwarding.secret } ] }
        - name: work
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata: { name: lobby, namespace: mc }
spec:
  selector: { app: lobby }
  ports: [ { port: 25565, targetPort: 25565 } ]
```

- [ ] **Step 2: velocity manifest**

`deploy/k8s/velocity.yaml`:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: velocity, namespace: mc }
spec:
  replicas: 1   # ponytail: 1 now; design assumes N. Multi-replica registry sync is Slice 1.
  selector: { matchLabels: { app: velocity } }
  template:
    metadata: { labels: { app: velocity } }
    spec:
      containers:
        - name: velocity
          image: mc/velocity:dev
          imagePullPolicy: IfNotPresent
          ports: [ { containerPort: 25565 } ]
          volumeMounts:
            - { name: secret, mountPath: /secret, readOnly: true }
          readinessProbe:
            tcpSocket: { port: 25565 }
            initialDelaySeconds: 8
            periodSeconds: 5
      volumes:
        - name: secret
          secret: { secretName: velocity-forwarding, items: [ { key: forwarding.secret, path: forwarding.secret } ] }
---
apiVersion: v1
kind: Service
metadata: { name: velocity, namespace: mc }
spec:
  type: LoadBalancer
  selector: { app: velocity }
  ports: [ { port: 25565, targetPort: 25565, protocol: TCP } ]
```

- [ ] **Step 3: Makefile integration targets**

```makefile
.PHONY: load apply up smoke
load: build-velocity build-lobby
	kind load docker-image mc/velocity:dev --name $(CLUSTER)
	kind load docker-image mc/lobby:dev --name $(CLUSTER)
apply:
	kubectl -n mc create secret generic velocity-forwarding \
	  --from-literal=forwarding.secret=$$(openssl rand -hex 24) \
	  --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f deploy/k8s/velocity.yaml -f deploy/k8s/lobby.yaml
up: cluster load apply
	@echo "waiting for LoadBalancer IP..."; \
	  kubectl -n mc get svc velocity -w &
smoke:
	@IP=$$(kubectl -n mc get svc velocity -o jsonpath='{.status.loadBalancer.ingress[0].ip}'); \
	  echo "pinging $$IP:25565"; go run ./tools/smoke $$IP 25565
```
The secret is generated once with `openssl rand`; both Deployments mount the same Secret so the forwarding secret matches on both sides.

- [ ] **Step 4: Full bring-up + smoke**

Run:
```bash
make up
# wait until both deployments are Ready and velocity Service has an EXTERNAL-IP
kubectl -n mc rollout status deploy/lobby
kubectl -n mc rollout status deploy/velocity
make smoke
```
Expected: `make smoke` prints `OK: proxy answered status ping` and exits 0.

- [ ] **Step 5: Manual confirmation (documented in README)**

Connect a real Minecraft 1.21.x client to `<velocity EXTERNAL-IP>:25565`. Expected: spawn in the lobby slime world, invulnerable, flying, compass in slot 0 opens the Minigames GUI; clicking an entry shows the Slice-1 stub message.

- [ ] **Step 6: Write README + commit**

`README.md`: prerequisites (`kind`, `kubectl`, `docker`, `go`, `gradle`, `cloud-provider-kind` via `go install sigs.k8s.io/cloud-provider-kind@latest`), the version-pinning step, `make lobby-world` once, then `make up` / `make smoke` / `make down`, and the manual connect step.
```bash
git add -A && git commit -m "feat(deploy): k8s manifests + make up/smoke integration; README"
```

---

## Self-Review

**Spec coverage:** kind+LB (T1), cloud-provider-kind (T1), velocity custom image + modern forwarding (T2), mc-base ASP + SWM file source + secret fail-fast (T3), baked lobby.slime (T4), lobby plugin invuln/flight/compass GUI + stub click (T5), lobby image FROM mc-base (T6), smoke server-list ping (T7), LB Service + ClusterIP + Secret + emptyDir + `make up`/`smoke` + manual confirm (T8). All Slice-0 spec sections map to a task. Deferrals (controller, multi-replica sync, central world store, UDP, readOnlyRootFS, GUI framework) are left unbuilt and marked.

**Placeholder scan:** The only intentional `<...>` are version URLs in `versions.env` and the ASP SWM config schema — both have an explicit "resolve to concrete value now / reconcile against pinned docs" step (T1S1, T3S1). No "TBD/add error handling/similar to" placeholders elsewhere.

**Type consistency:** `Menu.parse(List<Map<String,Object>>) -> List<Menu.Entry>` with `Entry(name, material, target)` used identically in T5 test, impl, and `LobbyPlugin`. `writeVarInt(*bytes.Buffer, int)` consistent across T7 test + impl. Secret path `/secret/forwarding.secret`, Secret name `velocity-forwarding`, namespace `mc`, image tags `mc/*:dev` consistent across T2/T3/T6/T8.
