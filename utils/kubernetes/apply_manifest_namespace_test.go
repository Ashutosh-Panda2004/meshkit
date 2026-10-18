package kubernetes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// nsCapture records the requests an ApplyManifest call makes against a
// fake Kubernetes API server.
type nsCapture struct {
	mu       sync.Mutex
	posts    []string
	lastBody string
	nsBody   string
}

func (c *nsCapture) handler() http.Handler {
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, body string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"kind":"APIVersions","apiVersion":"v1","versions":["v1"],"serverAddressByClientCIDRs":[]}`)
	})
	mux.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"v1","resources":[{"name":"namespaces","singularName":"","namespaced":false,"kind":"Namespace","verbs":["create","get","list","delete"]}]}`)
	})
	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"kind":"APIGroupList","apiVersion":"v1","groups":[{"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`)
	})
	mux.HandleFunc("/apis/apps/v1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, `{"kind":"APIResourceList","apiVersion":"v1","groupVersion":"apps/v1","resources":[{"name":"deployments","singularName":"","namespaced":true,"kind":"Deployment","verbs":["create","get","list","update","delete"]}]}`)
	})
	mux.HandleFunc("/api/v1/namespaces", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.nsBody = string(body)
		c.mu.Unlock()
		writeJSON(w, 201, string(body))
	})
	mux.HandleFunc("/apis/apps/v1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.posts = append(c.posts, r.URL.Path)
		c.lastBody = string(body)
		c.mu.Unlock()
		writeJSON(w, 201, string(body))
	})
	return mux
}

func nsDeploymentManifest(ns string) string {
	return fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: web
          image: nginx:1.25
`, ns)
}

func runApplyAgainstFakeServer(t *testing.T, manifestNS, optionNS string) (*nsCapture, error) {
	t.Helper()
	capture := &nsCapture{}
	srv := httptest.NewServer(capture.handler())
	t.Cleanup(srv.Close)
	cfg := &rest.Config{Host: srv.URL, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("clientset: %v", err)
	}
	client := &Client{RestConfig: *cfg, KubeClient: cs}
	err = client.ApplyManifest([]byte(nsDeploymentManifest(manifestNS)), ApplyOptions{Namespace: optionNS})
	return capture, err
}

func (c *nsCapture) postedNamespace() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.posts {
		parts := strings.Split(p, "/")
		// /apis/apps/v1/namespaces/<ns>/deployments
		if len(parts) == 7 && parts[4] == "namespaces" {
			return parts[5]
		}
	}
	return ""
}

func (c *nsCapture) objectNamespace() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(c.lastBody), &obj); err != nil {
		return ""
	}
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		ns, _ := md["namespace"].(string)
		return ns
	}
	return ""
}

// The namespace in ApplyOptions wins whenever one is passed, and the
// manifest namespace is used when none is passed, whatever their lengths:
// a single-character namespace is a valid Kubernetes namespace.
func TestApplyManifestNamespaceLength(t *testing.T) {
	tests := []struct {
		name       string
		manifestNS string
		optionNS   string
		wantNS     string
	}{
		{name: "single-character option overrides manifest namespace", manifestNS: "prod", optionNS: "a", wantNS: "a"},
		{name: "two-character option overrides manifest namespace", manifestNS: "prod", optionNS: "ab", wantNS: "ab"},
		{name: "longer option overrides single-character manifest namespace", manifestNS: "a", optionNS: "prod", wantNS: "prod"},
		{name: "single-character manifest namespace kept without option", manifestNS: "a", optionNS: "", wantNS: "a"},
		{name: "default manifest namespace kept without option", manifestNS: "default", optionNS: "", wantNS: "default"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture, err := runApplyAgainstFakeServer(t, tc.manifestNS, tc.optionNS)
			if err != nil {
				t.Fatalf("apply failed: %v", err)
			}
			if got := capture.postedNamespace(); got != tc.wantNS {
				t.Errorf("deployment posted to namespace %q, want %q", got, tc.wantNS)
			}
			if got := capture.objectNamespace(); got != tc.wantNS {
				t.Errorf("object namespace is %q, want %q", got, tc.wantNS)
			}
		})
	}
}
