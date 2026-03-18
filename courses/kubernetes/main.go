package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Simple in-memory store: resource/namespace/name → JSON bytes
type Store struct {
	mu   sync.RWMutex
	data map[string][]byte // key: "resource/namespace/name"
	logs map[string]string // key: "namespace/podname" → log text
}

var store = &Store{
	data: make(map[string][]byte),
	logs: make(map[string]string),
}

func (s *Store) key(resource, ns, name string) string {
	return resource + "/" + ns + "/" + name
}

func (s *Store) Get(resource, ns, name string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[s.key(resource, ns, name)]
	return v, ok
}

func (s *Store) List(resource, ns string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := resource + "/" + ns + "/"
	var items []json.RawMessage
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			items = append(items, json.RawMessage(v))
		}
	}
	return items
}

func (s *Store) ListAll(resource string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := resource + "/"
	var items []json.RawMessage
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			items = append(items, json.RawMessage(v))
		}
	}
	return items
}

func (s *Store) Put(resource, ns, name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(resource, ns, name)] = data
}

func (s *Store) Delete(resource, ns, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := s.key(resource, ns, name)
	if _, ok := s.data[k]; ok {
		delete(s.data, k)
		return true
	}
	return false
}

func main() {
	initData()
	mux := http.NewServeMux()
	mux.HandleFunc("/", router)
	log.Println("Mock Kubernetes API server listening on 127.0.0.1:8080")
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", mux))
}

func router(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimSuffix(r.URL.Path, "/")

	switch path {
	case "/api":
		w.Write([]byte(discoveryAPI))
		return
	case "/api/v1":
		w.Write([]byte(discoveryV1))
		return
	case "/apis":
		w.Write([]byte(discoveryAPIs))
		return
	case "/apis/apps", "/apis/apps/v1":
		w.Write([]byte(discoveryAppsV1))
		return
	case "/apis/argoproj.io", "/apis/argoproj.io/v1alpha1":
		w.Write([]byte(discoveryArgoprojV1alpha1))
		return
	case "/api/v1/nodes":
		items := store.ListAll("nodes")
		if wantsTable(r) {
			writeTableList(w, "nodes", items)
		} else {
			writeList(w, "v1", "NodeList", items)
		}
		return
	case "/version":
		w.Write([]byte(versionJSON))
		return
	case "/api/v1/namespaces":
		if r.Method == "POST" {
			handleCreate(w, r, "namespaces", "", path)
		} else {
			handleListNamespaces(w, r)
		}
		return
	}

	// openapi - kubectl asks for this, just 404
	if strings.HasPrefix(path, "/openapi") {
		http.NotFound(w, r)
		return
	}

	// Parse resource paths
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")

	// Cluster-wide list: /api/v1/{resource} (e.g. /api/v1/pods for --all-namespaces)
	if len(parts) == 3 && parts[0] == "api" && parts[1] == "v1" && parts[2] != "namespaces" {
		resource := parts[2]
		items := store.ListAll(resource)
		writeList(w, "v1", kindForResource(resource)+"List", items)
		return
	}

	// Cluster-wide list: /apis/{group}/{version}/{resource}
	if len(parts) == 4 && parts[0] == "apis" && parts[3] != "namespaces" {
		resource := parts[3]
		items := store.ListAll(resource)
		writeList(w, parts[1]+"/"+parts[2], kindForResource(resource)+"List", items)
		return
	}

	// /api/v1/nodes/{name}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "nodes" {
		handleSingleResource(w, r, "nodes", "", parts[3])
		return
	}

	// /api/v1/namespaces/{ns}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "namespaces" {
		ns := parts[3]
		handleSingleResource(w, r, "namespaces", "", ns)
		return
	}

	// /api/v1/namespaces/{ns}/{resource}[/{name}[/log]]
	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "namespaces" {
		ns := parts[3]
		resource := parts[4]
		if len(parts) == 5 {
			if r.Method == "POST" {
				handleCreate(w, r, resource, ns, path)
			} else {
				handleListResource(w, r, resource, ns)
			}
			return
		}
		if len(parts) == 6 {
			name := parts[5]
			handleSingleResource(w, r, resource, ns, name)
			return
		}
		if len(parts) == 7 && parts[6] == "log" {
			handleLogs(w, r, ns, parts[5])
			return
		}
	}

	// /apis/{group}/{version}/namespaces/{ns}/{resource}[/{name}]
	if len(parts) >= 6 && parts[0] == "apis" && parts[3] == "namespaces" {
		ns := parts[4]
		resource := parts[5]
		if len(parts) == 6 {
			if r.Method == "POST" {
				handleCreate(w, r, resource, ns, path)
			} else {
				handleListResource(w, r, resource, ns)
			}
			return
		}
		if len(parts) == 7 {
			name := parts[6]
			handleSingleResource(w, r, resource, ns, name)
			return
		}
	}

	http.NotFound(w, r)
}

func handleListNamespaces(w http.ResponseWriter, _ *http.Request) {
	items := store.List("namespaces", "")
	writeList(w, "v1", "NamespaceList", items)
}

func handleListResource(w http.ResponseWriter, r *http.Request, resource, ns string) {
	items := store.List(resource, ns)
	if wantsTable(r) {
		if cols, _ := tableColumnsForResource(resource); cols != nil {
			writeTableList(w, resource, items)
			return
		}
	}
	apiVersion := "v1"
	kind := kindForResource(resource) + "List"
	if resource == "deployments" || resource == "statefulsets" {
		apiVersion = "apps/v1"
	}
	if resource == "applications" {
		apiVersion = "argoproj.io/v1alpha1"
	}
	writeList(w, apiVersion, kind, items)
}

func handleSingleResource(w http.ResponseWriter, r *http.Request, resource, ns, name string) {
	switch r.Method {
	case "GET":
		data, ok := store.Get(resource, ns, name)
		if !ok {
			writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
			return
		}
		w.Write(data)
	case "PUT", "PATCH":
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			writeStatus(w, 400, "empty body")
			return
		}
		store.Put(resource, ns, name, body)
		w.Write(body)
	case "DELETE":
		if store.Delete(resource, ns, name) {
			writeStatus(w, 200, fmt.Sprintf("%s %q deleted", resource, name))
		} else {
			writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
		}
	default:
		writeStatus(w, 405, "method not allowed")
	}
}

func handleCreate(w http.ResponseWriter, r *http.Request, resource, ns, _ string) {
	body, _ := io.ReadAll(r.Body)
	if len(body) == 0 {
		writeStatus(w, 400, "empty body")
		return
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		writeStatus(w, 400, "invalid JSON")
		return
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
		obj["metadata"] = meta
	}
	name, _ := meta["name"].(string)
	if name == "" {
		writeStatus(w, 400, "metadata.name required")
		return
	}
	// Auto-set server-side fields
	if _, ok := meta["creationTimestamp"]; !ok {
		meta["creationTimestamp"] = time.Now().Format(time.RFC3339)
	}
	if _, ok := meta["uid"]; !ok {
		meta["uid"] = uid()
	}
	if _, ok := meta["resourceVersion"]; !ok {
		meta["resourceVersion"] = "5000"
	}
	// For namespaces, ns is empty (cluster-scoped)
	if resource == "namespaces" {
		ns = ""
	}
	enriched, _ := json.Marshal(obj)
	store.Put(resource, ns, name, enriched)
	w.WriteHeader(201)
	w.Write(enriched)
}

func handleLogs(w http.ResponseWriter, _ *http.Request, ns, podName string) {
	w.Header().Set("Content-Type", "text/plain")
	store.mu.RLock()
	logText, ok := store.logs[ns+"/"+podName]
	store.mu.RUnlock()
	if !ok {
		w.Write([]byte("No logs available for this pod.\n"))
		return
	}
	w.Write([]byte(logText))
}

func wantsTable(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "as=Table")
}

func writeList(w http.ResponseWriter, apiVersion, kind string, items []json.RawMessage) {
	if items == nil {
		items = []json.RawMessage{}
	}
	resp := map[string]interface{}{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   map[string]interface{}{"resourceVersion": "1000"},
		"items":      items,
	}
	json.NewEncoder(w).Encode(resp)
}

func writeTableList(w http.ResponseWriter, resource string, items []json.RawMessage) {
	columns, rowFn := tableColumnsForResource(resource)
	rows := make([]interface{}, 0, len(items))
	for _, item := range items {
		var obj map[string]interface{}
		json.Unmarshal(item, &obj)
		rows = append(rows, map[string]interface{}{
			"cells":  rowFn(obj),
			"object": map[string]interface{}{"apiVersion": "v1", "kind": kindForResource(resource), "metadata": obj["metadata"]},
		})
	}
	resp := map[string]interface{}{
		"apiVersion": "meta.k8s.io/v1",
		"kind":       "Table",
		"metadata":   map[string]interface{}{"resourceVersion": "1000"},
		"columnDefinitions": columns,
		"rows":              rows,
	}
	json.NewEncoder(w).Encode(resp)
}

func tableColumnsForResource(resource string) ([]interface{}, func(map[string]interface{}) []interface{}) {
	col := func(name, typ string) interface{} {
		return map[string]string{"name": name, "type": typ}
	}

	switch resource {
	case "nodes":
		return []interface{}{
			col("Name", "string"), col("Status", "string"), col("Roles", "string"),
			col("Age", "string"), col("Version", "string"),
			col("Internal-IP", "string"), col("OS-Image", "string"),
			col("Kernel-Version", "string"), col("Container-Runtime", "string"),
		}, func(o map[string]interface{}) []interface{} {
			status := nested(o, "status")
			nodeInfo := nested(status, "nodeInfo")
			// Status from conditions
			st := "NotReady"
			if conds, ok := status["conditions"].([]interface{}); ok {
				for _, c := range conds {
					cm, _ := c.(map[string]interface{})
					if cm["type"] == "Ready" && cm["status"] == "True" {
						st = "Ready"
					}
				}
			}
			// Roles from labels
			meta := nested(o, "metadata")
			labels, _ := meta["labels"].(map[string]interface{})
			var roles []string
			if _, ok := labels["node-role.kubernetes.io/control-plane"]; ok {
				roles = append(roles, "control-plane")
			}
			roleStr := "<none>"
			if len(roles) > 0 {
				roleStr = strings.Join(roles, ",")
			}
			// IP
			ip := "<none>"
			if addrs, ok := status["addresses"].([]interface{}); ok {
				for _, a := range addrs {
					am, _ := a.(map[string]interface{})
					if am["type"] == "InternalIP" {
						ip, _ = am["address"].(string)
					}
				}
			}
			return []interface{}{
				str(meta, "name"), st, roleStr,
				str(meta, "creationTimestamp"), str(nodeInfo, "kubeletVersion"),
				ip, str(nodeInfo, "osImage"),
				str(nodeInfo, "kernelVersion"), str(nodeInfo, "containerRuntimeVersion"),
			}
		}

	case "applications":
		return []interface{}{
			col("Name", "string"), col("Sync Status", "string"),
			col("Health Status", "string"), col("Project", "string"),
			col("Age", "string"),
		}, func(o map[string]interface{}) []interface{} {
			meta := nested(o, "metadata")
			spec := nested(o, "spec")
			status := nested(o, "status")
			syncStatus := nested(status, "sync")
			healthStatus := nested(status, "health")
			return []interface{}{
				str(meta, "name"), str(syncStatus, "status"),
				str(healthStatus, "status"), str(spec, "project"),
				str(meta, "creationTimestamp"),
			}
		}

	default:
		return nil, nil
	}
}

func nested(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	v, _ := m[key].(map[string]interface{})
	if v == nil {
		return map[string]interface{}{}
	}
	return v
}

func str(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	resp := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Status",
		"metadata":   map[string]interface{}{},
		"status":     "Failure",
		"message":    msg,
		"code":       code,
	}
	json.NewEncoder(w).Encode(resp)
}

func kindForResource(resource string) string {
	kinds := map[string]string{
		"pods": "Pod", "services": "Service", "configmaps": "ConfigMap",
		"secrets": "Secret", "namespaces": "Namespace", "nodes": "Node",
		"deployments": "Deployment", "statefulsets": "StatefulSet",
		"applications": "Application",
	}
	if k, ok := kinds[resource]; ok {
		return k
	}
	return "Unknown"
}

// --- Discovery responses ---

const versionJSON = `{"major":"1","minor":"34","gitVersion":"v1.34.0","platform":"linux/arm64"}`

const discoveryAPI = `{"kind":"APIVersions","versions":["v1"],"serverAddressByClientCIDRs":[{"clientCIDR":"0.0.0.0/0","serverAddress":"127.0.0.1:8080"}]}`

const discoveryV1 = `{
  "kind":"APIResourceList","groupVersion":"v1",
  "resources":[
    {"name":"namespaces","singularName":"namespace","namespaced":false,"kind":"Namespace","shortNames":["ns"],"verbs":["get","list","create","update","patch","delete"]},
    {"name":"nodes","singularName":"node","namespaced":false,"kind":"Node","shortNames":["no"],"verbs":["get","list"]},
    {"name":"pods","singularName":"pod","namespaced":true,"kind":"Pod","verbs":["get","list","create","update","patch","delete"]},
    {"name":"pods/log","singularName":"","namespaced":true,"kind":"Pod","verbs":["get"]},
    {"name":"services","singularName":"service","namespaced":true,"kind":"Service","verbs":["get","list","create","update","patch","delete"]},
    {"name":"configmaps","singularName":"configmap","namespaced":true,"kind":"ConfigMap","verbs":["get","list","create","update","patch","delete"]},
    {"name":"secrets","singularName":"secret","namespaced":true,"kind":"Secret","verbs":["get","list","create","update","patch","delete"]}
  ]}`

const discoveryAPIs = `{
  "kind":"APIGroupList",
  "groups":[
    {"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}},
    {"name":"argoproj.io","versions":[{"groupVersion":"argoproj.io/v1alpha1","version":"v1alpha1"}],"preferredVersion":{"groupVersion":"argoproj.io/v1alpha1","version":"v1alpha1"}}
  ]}`

const discoveryAppsV1 = `{
  "kind":"APIResourceList","groupVersion":"apps/v1",
  "resources":[
    {"name":"deployments","singularName":"deployment","namespaced":true,"kind":"Deployment","verbs":["get","list","create","update","patch","delete"]},
    {"name":"statefulsets","singularName":"statefulset","namespaced":true,"kind":"StatefulSet","verbs":["get","list","create","update","patch","delete"]}
  ]}`

const discoveryArgoprojV1alpha1 = `{
  "kind":"APIResourceList","groupVersion":"argoproj.io/v1alpha1",
  "resources":[
    {"name":"applications","singularName":"application","namespaced":true,"kind":"Application","shortNames":["app","apps"],"verbs":["get","list","create","update","patch","delete"]}
  ]}`

// --- Pre-populated cluster data ---

func initData() {
	ts := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	ts2 := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)

	// Namespaces
	for _, ns := range []string{"default", "kube-system", "production"} {
		store.Put("namespaces", "", ns, obj(`{
			"apiVersion":"v1","kind":"Namespace",
			"metadata":{"name":"`+ns+`","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"100"},
			"status":{"phase":"Active"}}`))
	}

	// --- kube-system ---
	store.Put("pods", "kube-system", "coredns-5dd5756b68-xk9r2", obj(`{
		"apiVersion":"v1","kind":"Pod",
		"metadata":{"name":"coredns-5dd5756b68-xk9r2","namespace":"kube-system","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"200",
			"labels":{"k8s-app":"kube-dns","pod-template-hash":"5dd5756b68"}},
		"spec":{"containers":[{"name":"coredns","image":"registry.k8s.io/coredns/coredns:v1.11.1","ports":[{"containerPort":53,"protocol":"UDP"},{"containerPort":53,"protocol":"TCP"}]}]},
		"status":{"phase":"Running","podIP":"10.244.0.2","hostIP":"192.168.1.10",
			"containerStatuses":[{"name":"coredns","ready":true,"restartCount":0,"image":"registry.k8s.io/coredns/coredns:v1.11.1",
				"state":{"running":{"startedAt":"`+ts+`"}}}],
			"conditions":[{"type":"Ready","status":"True"}]}}`))

	store.logs["kube-system/coredns-5dd5756b68-xk9r2"] = `[INFO] plugin/reload: Running configuration SHA512 = abc123def456
[INFO] CoreDNS-1.11.1
[INFO] linux/arm64, go1.21.8
[INFO] plugin/kubernetes: starting with in-cluster config
`

	// --- default namespace ---
	for i, suffix := range []string{"abc12", "def34"} {
		ip := fmt.Sprintf("10.244.0.%d", 5+i)
		store.Put("pods", "default", "nginx-7d4f8b7b94-"+suffix, obj(`{
			"apiVersion":"v1","kind":"Pod",
			"metadata":{"name":"nginx-7d4f8b7b94-`+suffix+`","namespace":"default","uid":"`+uid()+`","creationTimestamp":"`+ts2+`","resourceVersion":"300",
				"labels":{"app":"nginx","pod-template-hash":"7d4f8b7b94"},
				"ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"nginx-7d4f8b7b94"}]},
			"spec":{"containers":[{"name":"nginx","image":"nginx:1.25","ports":[{"containerPort":80}],
				"resources":{"requests":{"cpu":"100m","memory":"128Mi"},"limits":{"cpu":"250m","memory":"256Mi"}}}]},
			"status":{"phase":"Running","podIP":"`+ip+`","hostIP":"192.168.1.10",
				"containerStatuses":[{"name":"nginx","ready":true,"restartCount":0,"image":"nginx:1.25",
					"state":{"running":{"startedAt":"`+ts2+`"}}}],
				"conditions":[{"type":"Ready","status":"True"}]}}`))

		store.logs["default/nginx-7d4f8b7b94-"+suffix] = fmt.Sprintf(`172.17.0.1 - - [15/Mar/2026:10:00:01 +0000] "GET / HTTP/1.1" 200 615 "-" "curl/8.5.0"
172.17.0.1 - - [15/Mar/2026:10:00:02 +0000] "GET /healthz HTTP/1.1" 200 2 "-" "kube-probe/1.31"
172.17.0.1 - - [15/Mar/2026:10:05:01 +0000] "GET / HTTP/1.1" 200 615 "-" "Mozilla/5.0"
172.17.0.1 - - [15/Mar/2026:10:10:02 +0000] "GET /healthz HTTP/1.1" 200 2 "-" "kube-probe/1.31"
`)
	}

	store.Put("deployments", "default", "nginx", obj(`{
		"apiVersion":"apps/v1","kind":"Deployment",
		"metadata":{"name":"nginx","namespace":"default","uid":"`+uid()+`","creationTimestamp":"`+ts2+`","resourceVersion":"400",
			"labels":{"app":"nginx"}},
		"spec":{"replicas":2,"selector":{"matchLabels":{"app":"nginx"}},
			"template":{"metadata":{"labels":{"app":"nginx"}},
				"spec":{"containers":[{"name":"nginx","image":"nginx:1.25","ports":[{"containerPort":80}],
					"resources":{"requests":{"cpu":"100m","memory":"128Mi"},"limits":{"cpu":"250m","memory":"256Mi"}}}]}}},
		"status":{"replicas":2,"readyReplicas":2,"availableReplicas":2,"updatedReplicas":2}}`))

	store.Put("services", "default", "nginx", obj(`{
		"apiVersion":"v1","kind":"Service",
		"metadata":{"name":"nginx","namespace":"default","uid":"`+uid()+`","creationTimestamp":"`+ts2+`","resourceVersion":"500"},
		"spec":{"type":"ClusterIP","clusterIP":"10.96.100.50","ports":[{"port":80,"targetPort":80,"protocol":"TCP"}],
			"selector":{"app":"nginx"}}}`))

	store.Put("configmaps", "default", "app-config", obj(`{
		"apiVersion":"v1","kind":"ConfigMap",
		"metadata":{"name":"app-config","namespace":"default","uid":"`+uid()+`","creationTimestamp":"`+ts2+`","resourceVersion":"600"},
		"data":{"LOG_LEVEL":"info","MAX_CONNECTIONS":"100","FEATURE_FLAGS":"dark-mode=true,beta-api=false"}}`))

	// --- production namespace ---
	for i, suffix := range []string{"ghi56", "jkl78", "mno90"} {
		ip := fmt.Sprintf("10.244.1.%d", 10+i)
		store.Put("pods", "production", "api-server-6b8f9c7d-"+suffix, obj(`{
			"apiVersion":"v1","kind":"Pod",
			"metadata":{"name":"api-server-6b8f9c7d-`+suffix+`","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"700",
				"labels":{"app":"api-server","pod-template-hash":"6b8f9c7d"}},
			"spec":{"containers":[{"name":"api","image":"mycompany/api-server:2.1.0","ports":[{"containerPort":8080}],
				"envFrom":[{"configMapRef":{"name":"api-config"}},{"secretRef":{"name":"db-credentials"}}]}]},
			"status":{"phase":"Running","podIP":"`+ip+`","hostIP":"192.168.1.11",
				"containerStatuses":[{"name":"api","ready":true,"restartCount":`+fmt.Sprintf("%d", i)+`,"image":"mycompany/api-server:2.1.0",
					"state":{"running":{"startedAt":"`+ts+`"}}}],
				"conditions":[{"type":"Ready","status":"True"}]}}`))
	}

	store.Put("pods", "production", "postgres-0", obj(`{
		"apiVersion":"v1","kind":"Pod",
		"metadata":{"name":"postgres-0","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"800",
			"labels":{"app":"postgres","statefulset.kubernetes.io/pod-name":"postgres-0"}},
		"spec":{"containers":[{"name":"postgres","image":"postgres:16","ports":[{"containerPort":5432}],
			"env":[{"name":"POSTGRES_DB","value":"appdb"},{"name":"POSTGRES_PASSWORD","valueFrom":{"secretKeyRef":{"name":"db-credentials","key":"password"}}}],
			"volumeMounts":[{"name":"data","mountPath":"/var/lib/postgresql/data"}]}]},
		"status":{"phase":"Running","podIP":"10.244.1.20","hostIP":"192.168.1.11",
			"containerStatuses":[{"name":"postgres","ready":true,"restartCount":0,"image":"postgres:16",
				"state":{"running":{"startedAt":"`+ts+`"}}}],
			"conditions":[{"type":"Ready","status":"True"}]}}`))

	store.logs["production/postgres-0"] = `2026-03-15 10:00:00.000 UTC [1] LOG:  starting PostgreSQL 16.2 on aarch64-unknown-linux-musl
2026-03-15 10:00:00.100 UTC [1] LOG:  listening on IPv4 address "0.0.0.0", port 5432
2026-03-15 10:00:00.200 UTC [1] LOG:  database system is ready to accept connections
2026-03-16 02:00:00.000 UTC [1234] LOG:  checkpoint starting: time
2026-03-16 02:00:05.000 UTC [1234] LOG:  checkpoint complete
`

	store.Put("deployments", "production", "api-server", obj(`{
		"apiVersion":"apps/v1","kind":"Deployment",
		"metadata":{"name":"api-server","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"900",
			"labels":{"app":"api-server"}},
		"spec":{"replicas":3,"selector":{"matchLabels":{"app":"api-server"}},
			"template":{"metadata":{"labels":{"app":"api-server"}},
				"spec":{"containers":[{"name":"api","image":"mycompany/api-server:2.1.0","ports":[{"containerPort":8080}],
					"envFrom":[{"configMapRef":{"name":"api-config"}},{"secretRef":{"name":"db-credentials"}}]}]}}},
		"status":{"replicas":3,"readyReplicas":3,"availableReplicas":3,"updatedReplicas":3}}`))

	store.Put("statefulsets", "production", "postgres", obj(`{
		"apiVersion":"apps/v1","kind":"StatefulSet",
		"metadata":{"name":"postgres","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"1000",
			"labels":{"app":"postgres"}},
		"spec":{"replicas":1,"serviceName":"postgres","selector":{"matchLabels":{"app":"postgres"}},
			"template":{"metadata":{"labels":{"app":"postgres"}},
				"spec":{"containers":[{"name":"postgres","image":"postgres:16","ports":[{"containerPort":5432}]}]}},
			"volumeClaimTemplates":[{"metadata":{"name":"data"},"spec":{"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"10Gi"}}}}]},
		"status":{"replicas":1,"readyReplicas":1,"currentReplicas":1}}`))

	store.Put("configmaps", "production", "api-config", obj(`{
		"apiVersion":"v1","kind":"ConfigMap",
		"metadata":{"name":"api-config","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"1100"},
		"data":{"DATABASE_HOST":"postgres.production.svc.cluster.local","DATABASE_PORT":"5432","DATABASE_NAME":"appdb","LOG_LEVEL":"warn","RATE_LIMIT":"1000"}}`))

	store.Put("secrets", "production", "db-credentials", obj(`{
		"apiVersion":"v1","kind":"Secret",
		"metadata":{"name":"db-credentials","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"1200"},
		"type":"Opaque",
		"data":{"username":"YWRtaW4=","password":"czNjcjN0LXBhc3N3MHJk"}}`))

	store.Put("services", "production", "postgres", obj(`{
		"apiVersion":"v1","kind":"Service",
		"metadata":{"name":"postgres","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"1300"},
		"spec":{"type":"ClusterIP","clusterIP":"10.96.200.10","ports":[{"port":5432,"targetPort":5432,"protocol":"TCP"}],
			"selector":{"app":"postgres"}}}`))

	store.Put("services", "production", "api-server", obj(`{
		"apiVersion":"v1","kind":"Service",
		"metadata":{"name":"api-server","namespace":"production","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"1400"},
		"spec":{"type":"ClusterIP","clusterIP":"10.96.200.20","ports":[{"port":80,"targetPort":8080,"protocol":"TCP"}],
			"selector":{"app":"api-server"}}}`))

	// --- Talos nodes ---
	nodeData := []struct {
		name, ip, podCIDR string
		master            bool
		ageDays           int
	}{
		{"talos-y5r-o5m", "192.168.1.10", "10.244.0.0/24", true, 172},
		{"talos-q7r-qa3", "192.168.1.11", "10.244.1.0/24", false, 172},
		{"talos-13q-0n4", "192.168.1.12", "10.244.2.0/24", false, 45},
	}
	for _, n := range nodeData {
		nodeTS := time.Now().Add(-time.Duration(n.ageDays) * 24 * time.Hour).Format(time.RFC3339)
		extraLabels := ""
		taints := "[]"
		if n.master {
			extraLabels = `,"node-role.kubernetes.io/control-plane":""`
			taints = `[{"key":"node-role.kubernetes.io/control-plane","effect":"NoSchedule"}]`
		}
		store.Put("nodes", "", n.name, obj(`{
			"apiVersion":"v1","kind":"Node",
			"metadata":{"name":"`+n.name+`","uid":"`+uid()+`","creationTimestamp":"`+nodeTS+`","resourceVersion":"2000",
				"labels":{"kubernetes.io/hostname":"`+n.name+`","kubernetes.io/os":"linux","kubernetes.io/arch":"arm64",
					"node.kubernetes.io/instance-type":"talos"`+extraLabels+`}},
			"spec":{"podCIDR":"`+n.podCIDR+`","taints":`+taints+`},
			"status":{
				"capacity":{"cpu":"4","memory":"8192Mi","pods":"110","ephemeral-storage":"50Gi"},
				"allocatable":{"cpu":"3800m","memory":"7680Mi","pods":"110","ephemeral-storage":"45Gi"},
				"conditions":[
					{"type":"Ready","status":"True","lastHeartbeatTime":"`+ts2+`","reason":"KubeletReady","message":"kubelet is posting ready status"},
					{"type":"MemoryPressure","status":"False"},
					{"type":"DiskPressure","status":"False"},
					{"type":"PIDPressure","status":"False"}],
				"addresses":[
					{"type":"InternalIP","address":"`+n.ip+`"},
					{"type":"Hostname","address":"`+n.name+`"}],
				"nodeInfo":{
					"machineID":"`+uid()+`",
					"kernelVersion":"6.6.54-talos",
					"osImage":"Talos (v1.8.1)",
					"containerRuntimeVersion":"containerd://2.0.0",
					"kubeletVersion":"v1.34.0",
					"kubeProxyVersion":"v1.34.0",
					"operatingSystem":"linux",
					"architecture":"arm64"}}}`))
	}

	// --- ArgoCD namespace and applications ---
	store.Put("namespaces", "", "argocd", obj(`{
		"apiVersion":"v1","kind":"Namespace",
		"metadata":{"name":"argocd","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"100",
			"labels":{"kubernetes.io/metadata.name":"argocd"}},
		"status":{"phase":"Active"}}`))

	argoApps := []struct {
		name, project, repoURL, path, destNs, syncStatus, healthStatus string
	}{
		{"nginx-web", "default", "https://github.com/example/infra.git", "apps/nginx", "default", "Synced", "Healthy"},
		{"api-server", "production", "https://github.com/example/infra.git", "apps/api-server", "production", "Synced", "Healthy"},
		{"postgres-db", "production", "https://github.com/example/infra.git", "apps/postgres", "production", "Synced", "Healthy"},
		{"monitoring", "default", "https://github.com/example/infra.git", "apps/monitoring", "monitoring", "OutOfSync", "Progressing"},
		{"cert-manager", "default", "https://github.com/jetstack/cert-manager.git", "deploy/charts/cert-manager", "cert-manager", "Synced", "Healthy"},
		{"ingress-nginx", "default", "https://github.com/kubernetes/ingress-nginx.git", "charts/ingress-nginx", "ingress-nginx", "Synced", "Degraded"},
	}
	for _, a := range argoApps {
		store.Put("applications", "argocd", a.name, obj(`{
			"apiVersion":"argoproj.io/v1alpha1","kind":"Application",
			"metadata":{"name":"`+a.name+`","namespace":"argocd","uid":"`+uid()+`","creationTimestamp":"`+ts+`","resourceVersion":"3000",
				"finalizers":["resources-finalizer.argocd.argoproj.io"]},
			"spec":{
				"project":"`+a.project+`",
				"source":{"repoURL":"`+a.repoURL+`","path":"`+a.path+`","targetRevision":"HEAD"},
				"destination":{"server":"https://kubernetes.default.svc","namespace":"`+a.destNs+`"},
				"syncPolicy":{"automated":{"prune":true,"selfHeal":true},"syncOptions":["CreateNamespace=true"]}},
			"status":{
				"sync":{"status":"`+a.syncStatus+`","revision":"abc123def456"},
				"health":{"status":"`+a.healthStatus+`"},
				"summary":{"images":["nginx:1.25","postgres:16","mycompany/api-server:2.1.0"]}}}`))
	}
}

func obj(s string) []byte {
	// Compact the JSON to remove whitespace
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		log.Fatalf("invalid JSON in initData: %v\nJSON: %s", err, s)
	}
	b, _ := json.Marshal(v)
	return b
}

var uidCounter int

func uid() string {
	uidCounter++
	return fmt.Sprintf("a%07d-b000-c000-d000-e00000%06d", uidCounter, uidCounter)
}
