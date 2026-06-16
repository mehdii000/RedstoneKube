include images/versions.env
export

CLUSTER := mc
CPKIND  := $(shell go env GOPATH)/bin/cloud-provider-kind
# Build the Paper plugin in a container so the host needs no gradle install.
# ponytail: dockerized gradle; install host gradle only if the container loop gets annoying.
GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/lobby-plugin:/work -w /work gradle:jdk21 gradle

.PHONY: cluster down build-velocity build-velreg build-base lobby-world plugin build-lobby build-controller build-minigame-stub load apply up smoke

# ---- cluster ----
cluster:
	kind create cluster --config deploy/kind/cluster.yaml
	kubectl apply -f deploy/k8s/namespace.yaml
	$(CPKIND) > /tmp/cpkind.log 2>&1 & echo $$! > /tmp/cpkind.pid
	@echo "cluster up; cloud-provider-kind pid $$(cat /tmp/cpkind.pid)"

down:
	-kill $$(cat /tmp/cpkind.pid) 2>/dev/null
	kind delete cluster --name $(CLUSTER)

# ---- images ----
VELREG_GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/velocity-register:/work -w /work gradle:jdk21 gradle

build-velreg:
	$(VELREG_GRADLE) build
	cp plugins/velocity-register/build/libs/velocity-register.jar images/velocity/velocity-register.jar

build-velocity: build-velreg
	docker build -t mc/velocity:dev \
	  --build-arg JRE_TAG=$(JRE_TAG) --build-arg VELOCITY_URL=$(VELOCITY_URL) \
	  images/velocity
	rm -f images/velocity/velocity-register.jar

build-base:
	docker build -t mc/mc-base:dev \
	  --build-arg JRE_TAG=$(JRE_TAG) --build-arg ASP_URL=$(ASP_URL) \
	  --build-arg ASP_PLUGIN_URL=$(ASP_PLUGIN_URL) \
	  images/mc-base

lobby-world:
	./images/mc-base/make-lobby-world.sh

plugin:
	$(GRADLE) build

build-lobby: build-base plugin
	cp plugins/lobby-plugin/build/libs/lobby-plugin.jar images/lobby/lobby-plugin.jar
	cp worlds/lobby.slime images/lobby/lobby.slime
	docker build -t mc/lobby:dev images/lobby
	rm -f images/lobby/lobby-plugin.jar images/lobby/lobby.slime

build-controller:
	docker build -f images/controller/Dockerfile -t mc/controller:dev .

STUB_GRADLE := docker run --rm -u $(shell id -u):$(shell id -g) \
  -e GRADLE_USER_HOME=/work/.gradle \
  -v $(PWD)/plugins/stub-game:/work -w /work gradle:jdk21 gradle

build-minigame-stub: build-base
	$(STUB_GRADLE) build
	cp plugins/stub-game/build/libs/stub-plugin.jar images/minigame-stub/stub-plugin.jar
	cp worlds/lobby.slime images/minigame-stub/game.slime
	docker build -t mc/minigame-stub:dev images/minigame-stub
	rm -f images/minigame-stub/stub-plugin.jar images/minigame-stub/game.slime

# ---- deploy ----
load: build-velocity build-lobby build-controller build-minigame-stub
	kind load docker-image mc/velocity:dev --name $(CLUSTER)
	kind load docker-image mc/lobby:dev --name $(CLUSTER)
	kind load docker-image mc/controller:dev --name $(CLUSTER)
	kind load docker-image mc/minigame-stub:dev --name $(CLUSTER)

apply:
	kubectl -n mc create secret generic velocity-forwarding \
	  --from-literal=forwarding.secret=$$(openssl rand -hex 24) \
	  --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f deploy/k8s/velocity.yaml -f deploy/k8s/lobby.yaml

up: cluster load apply
	@echo "waiting for LoadBalancer IP (Ctrl-C once assigned)..."
	kubectl -n mc get svc velocity -w

smoke:
	@IP=$$(kubectl -n mc get svc velocity -o jsonpath='{.status.loadBalancer.ingress[0].ip}'); \
	  echo "pinging $$IP:25565"; go run ./tools/smoke $$IP 25565
