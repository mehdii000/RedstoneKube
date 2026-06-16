# Slice 1 — Controller + dynamic registration (design)

Status: approved 2026-06-17. Built lazy (ponytail): stdlib/native first, shortest diff.

## Goal

Clicking a minigame in the lobby compass GUI spawns/joins a real, dynamically-spawned
minigame pod. The full disposable loop works end-to-end: pool of warm pods → join →
game ends → pod killed → pool refilled.

This is the plumbing slice. There is **no real minigame yet** (the minigame image
convention is Slice 2); Slice 1 spawns one concrete placeholder game to prove the loop.

## Architecture (4 components)

| Component | New? | Responsibility |
|---|---|---|
| **controller** (Go) | NEW | Owns pod truth + warm pool. In-cluster k8s client. REST API. |
| **velocity-register** plugin | NEW | Dumb executor: register/unregister a server with Velocity over HTTP. |
| **lobby** plugin | EDIT `onClick` | Allocate an instance from controller → `Connect` the player. |
| **minigame-stub** image | NEW | One concrete placeholder game that can end itself. |

## Data flow (happy path)

```
boot:  controller reconcile → spawn POOL_SIZE stub pods → each becomes Ready
       → controller registers each with Velocity
play:  compass click → lobby POST /allocate {game:stub}
       → controller returns {server: mg-stub-abc, address: <podIP>:25565}
       → lobby sends BungeeCord Connect → Velocity moves the player (native)
end:   op runs /endgame → stub POST /instances/abc/done
       → controller unregisters from Velocity + deletes the pod
       → reconcile spawns a replacement → registers it
```

## Lifecycle model

Warm pool of ready-to-join pods, each game session **disposable**: when a game ends,
that pod is killed and a fresh one is spawned to keep the pool at `POOL_SIZE`.
On-demand is just `POOL_SIZE = 0`; the pool is not special-cased.

## Controller (Go, stdlib `net/http` + client-go)

Runs as a pod in namespace `mc` with an in-cluster k8s client.

### REST API
- `POST /allocate` body `{"game": "stub"}` → picks a Ready, unallocated pod of that
  game, marks it allocated (label `alloc=true`), triggers a refill, returns
  `{"server": "<podName>", "address": "<podIP>:25565"}`. 409/503 if none ready.
- `POST /instances/{id}/done` → the pod self-reports game over → controller
  unregisters it from Velocity and deletes the pod. `{id}` is the pod name; controller
  validates it is a known minigame pod before acting.

### Reconcile loop
- Poll pods (label `app=minigame`) every ~2s. For each game type, maintain `POOL_SIZE`
  pods that are either Ready-unallocated or still booting.
  `ponytail: poll, not a k8s informer — upgrade when pod count / latency demands it.`
- On a pod becoming Ready → `POST` velocity-register `/servers`.
- On a pod disappearing → `DELETE` velocity-register `/servers/{name}`.
- Register is idempotent (upsert), so a retry on the next tick is safe; no bespoke
  retry/backoff machinery.

### Pure, testable core
- `plan(current, desired) → {create, delete}` — given observed pods and desired pool,
  returns which pods to create/delete. No k8s calls inside.
- `pickAllocatable(pods, game) → pod|nil` — selects a Ready, unallocated pod.
Both unit-tested without a cluster.

## Pod model

- **Bare Pods**, not Deployment/Job — the controller owns each pod's individual
  lifecycle (create/delete one at a time).
  `ponytail: bare pods; a Deployment makes replicas fungible, which disposable games aren't.`
- Name `mg-<game>-<rand>`; labels `app=minigame, game=<game>, alloc=<true|false>`.
- Velocity connects to **pod IP:25565** directly.
  `ponytail: pod IP, no per-pod Service — Service churn for disposable pods is pointless.`
- Readiness: TCP probe on 25565. Controller registers only Ready pods.
- `POOL_SIZE` env, default 1. Never hardcoded.

## velocity-register plugin

Baked into the velocity image. HTTP listener on `localhost:8080` inside the velocity pod.

- `POST /servers` body `{"name","address"}` → `proxy.registerServer(new ServerInfo(...))`.
  Re-registering the same name updates it (idempotent upsert).
- `DELETE /servers/{name}` → `proxy.unregisterServer(...)`.
- **Trust boundary:** a shared bearer token (k8s secret `controller-token`) checked on
  every request. No k8s access, no pool logic — a dumb executor.

## lobby plugin (edit `onClick`)

`onClick` currently stubs a chat message. Replace with:
- Map the clicked entry's `target` (already in `config.yml`, e.g. `bedwars`) to a game
  type; for Slice 1 the only game is `stub`.
- `POST` controller `/allocate` → get `server` name.
- Send a BungeeCord `Connect` plugin message with that name; Velocity moves the player.
- On failure (no instance / controller down) → chat message, player stays in lobby.

Controller URL via plugin config (`controller.mc.svc.cluster.local`).

## minigame-stub image

`FROM mc-base` (ASP) + a ~30-line stub-game plugin:
- Teleport joiners into a baked world (reuse the lobby slime-world pattern).
- Op command `/endgame` → `POST {CONTROLLER_URL}/instances/{INSTANCE_ID}/done`.
- `CONTROLLER_URL` and `INSTANCE_ID` come from env (controller sets `INSTANCE_ID` to the
  pod name when spawning).

One hardcoded game. **Not** a convention/generator — that is explicitly Slice 2.

## RBAC / deploy

- ServiceAccount `controller` + Role: pods `get,list,watch,create,delete` in `mc`;
  RoleBinding to the SA.
- Secret `controller-token` (random), mounted in both the controller and velocity pods.
- velocity-register baked into the velocity image build.
- New `make` targets: `build-controller`, `build-minigame-stub`. Both `kind load`ed and
  wired into `make load` / `make up`.
- New manifests: `deploy/k8s/controller.yaml` (Deployment + SA + Role + RoleBinding),
  and the `controller-token` secret created in the `apply` target (like
  `velocity-forwarding`).

## Runnable checks

- Go unit tests (assert/`testing`, no framework): `plan()` reconcile cases,
  `pickAllocatable()`, and the velocity-register HTTP handler against a fake registry.
- End-to-end: existing `make up` + manual compass click → join stub → `/endgame` →
  observe recycle. No MC integration harness (YAGNI).

## Deliberately deferred (NOT in Slice 1)

- Multi-replica Velocity registry sync — one proxy only. (`POOL_SIZE` proves nothing is
  hardcoded to 1, but cross-proxy sync is out.)
- Full token auth / rate limiting on `/done` beyond "pod name must be a known instance".
- Idle reclamation (no-players timeout) — game-end is explicit `/done` only.
- Per-game pool sizing config file — single `POOL_SIZE` for the one game.
- Anything in the Slice 2 minigame image convention.
