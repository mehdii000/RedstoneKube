package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const namespace = "mc"

// podManifest renders a bare minigame Pod as JSON. The pod runs the stub image,
// mounts the forwarding secret (it's an ASP backend), and learns its own identity
// + the controller URL from env so it can POST /done when its game ends.
// ponytail: JSON template string, not a typed PodSpec — no client-go, and the
// shape is fixed. Build a struct only if this manifest starts to branch.
func podManifest(name, game, image, controllerURL string) string {
	return fmt.Sprintf(`{
  "apiVersion":"v1","kind":"Pod",
  "metadata":{"name":%q,"namespace":%q,"labels":{"app":"minigame","game":%q,"alloc":"false"}},
  "spec":{
    "containers":[{
      "name":"minigame","image":%q,"imagePullPolicy":"IfNotPresent",
      "ports":[{"containerPort":25565}],
      "env":[{"name":"INSTANCE_ID","value":%q},{"name":"CONTROLLER_URL","value":%q}],
      "volumeMounts":[{"name":"secret","mountPath":"/secret","readOnly":true}],
      "readinessProbe":{"tcpSocket":{"port":25565},"initialDelaySeconds":20,"periodSeconds":5}
    }],
    "volumes":[{"name":"secret","secret":{"secretName":"velocity-forwarding","items":[{"key":"forwarding.secret","path":"forwarding.secret"}]}}]
  }
}`, name, namespace, game, image, name, controllerURL)
}

// kube is a minimal in-cluster k8s REST client. No client-go.
type kube struct {
	host, token string
	hc          *http.Client
}

func newKube() (*kube, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" {
		return nil, fmt.Errorf("not running in-cluster: KUBERNETES_SERVICE_HOST unset")
	}
	const base = "/var/run/secrets/kubernetes.io/serviceaccount"
	token, err := os.ReadFile(base + "/token")
	if err != nil {
		return nil, fmt.Errorf("read SA token: %w", err)
	}
	ca, err := os.ReadFile(base + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("bad CA cert")
	}
	return &kube{
		host:  fmt.Sprintf("https://%s:%s", host, port),
		token: strings.TrimSpace(string(token)),
		hc: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		},
	}, nil
}

func (k *kube) do(method, path, contentType, body string) ([]byte, int, error) {
	req, err := http.NewRequest(method, k.host+path, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+k.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := k.hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return b, resp.StatusCode, nil
}

func (k *kube) createPod(name, game, image, controllerURL string) error {
	b, code, err := k.do("POST", "/api/v1/namespaces/"+namespace+"/pods", "application/json", podManifest(name, game, image, controllerURL))
	if err != nil {
		return err
	}
	if code != 201 && code != 200 {
		return fmt.Errorf("createPod %s: %d %s", name, code, b)
	}
	return nil
}

func (k *kube) deletePod(name string) error {
	_, code, err := k.do("DELETE", "/api/v1/namespaces/"+namespace+"/pods/"+name, "", "")
	if err != nil {
		return err
	}
	if code != 200 && code != 202 && code != 404 {
		return fmt.Errorf("deletePod %s: %d", name, code)
	}
	return nil
}

func (k *kube) setAllocated(name string) error {
	patch := `{"metadata":{"labels":{"alloc":"true"}}}`
	_, code, err := k.do("PATCH", "/api/v1/namespaces/"+namespace+"/pods/"+name, "application/merge-patch+json", patch)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("setAllocated %s: %d", name, code)
	}
	return nil
}

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

// podLogs returns the last `tail` lines of a pod's log via the SA token.
func (k *kube) podLogs(name string, tail int) (string, error) {
	b, code, err := k.do("GET",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?tailLines=%d", namespace, name, tail),
		"", "")
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("podLogs %s: %d %s", name, code, b)
	}
	return string(b), nil
}

// parsePodList maps a k8s PodList into the controller's Pod view.
func parsePodList(body []byte) ([]Pod, error) {
	var pl struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				Labels            map[string]string `json:"labels"`
				CreationTimestamp time.Time         `json:"creationTimestamp"`
				DeletionTimestamp *time.Time        `json:"deletionTimestamp"`
			} `json:"metadata"`
			Status struct {
				PodIP      string `json:"podIP"`
				Conditions []struct {
					Type, Status       string
					LastTransitionTime time.Time `json:"lastTransitionTime"`
				} `json:"conditions"`
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
					State        struct {
						Waiting    *struct{ Reason, Message string } `json:"waiting"`
						Terminated *struct {
							Reason, Message string
							ExitCode        int `json:"exitCode"`
						} `json:"terminated"`
					} `json:"state"`
					LastState struct {
						Terminated *struct {
							Reason, Message string
							ExitCode        int `json:"exitCode"`
						} `json:"terminated"`
					} `json:"lastState"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &pl); err != nil {
		return nil, err
	}
	out := make([]Pod, 0, len(pl.Items))
	for _, it := range pl.Items {
		p := Pod{
			Name:     it.Metadata.Name,
			Game:     it.Metadata.Labels["game"],
			IP:       it.Status.PodIP,
			Alloc:    it.Metadata.Labels["alloc"] == "true",
			Created:  it.Metadata.CreationTimestamp,
			Deleting: it.Metadata.DeletionTimestamp != nil,
		}
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				p.Ready = true
				p.ReadyAt = c.LastTransitionTime
			}
		}
		if len(it.Status.ContainerStatuses) > 0 {
			cs := it.Status.ContainerStatuses[0]
			p.Restarts = cs.RestartCount
			if cs.State.Waiting != nil {
				p.WaitReason, p.WaitMsg = cs.State.Waiting.Reason, cs.State.Waiting.Message
			}
			if t := cs.LastState.Terminated; t != nil {
				p.TermReason, p.TermMsg, p.TermExit = t.Reason, t.Message, t.ExitCode
			} else if t := cs.State.Terminated; t != nil {
				p.TermReason, p.TermMsg, p.TermExit = t.Reason, t.Message, t.ExitCode
			}
		}
		out = append(out, p)
	}
	return out, nil
}
