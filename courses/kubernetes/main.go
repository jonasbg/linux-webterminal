package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu   sync.RWMutex
	data map[string][]byte
	logs map[string]string
}

type SeedData struct {
	Resources []json.RawMessage `json:"resources"`
	Logs      map[string]string `json:"logs"`
}

type NodeRuntime struct {
	Name      string
	HostIP    string
	PodCIDR   string
	Worker    bool
	NextOctet int
}

var store = &Store{
	data: make(map[string][]byte),
	logs: make(map[string]string),
}

var runtimeState = struct {
	mu            sync.Mutex
	nodes         []NodeRuntime
	nextNodeIndex int
	nextSuffix    int
	nextRV        int
}{
	nextSuffix: 100,
	nextRV:     5000,
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

func (s *Store) GetObject(resource, ns, name string) (map[string]interface{}, bool) {
	data, ok := s.Get(resource, ns, name)
	if !ok {
		return nil, false
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, false
	}
	return obj, true
}

func (s *Store) List(resource, ns string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := resource + "/" + ns + "/"
	items := make([]json.RawMessage, 0)
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			items = append(items, json.RawMessage(v))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return strMust(items[i], "metadata", "name") < strMust(items[j], "metadata", "name")
	})
	return items
}

func (s *Store) ListAll(resource string) []json.RawMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := resource + "/"
	items := make([]json.RawMessage, 0)
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			items = append(items, json.RawMessage(v))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return strMust(items[i], "metadata", "namespace")+"/"+strMust(items[i], "metadata", "name") <
			strMust(items[j], "metadata", "namespace")+"/"+strMust(items[j], "metadata", "name")
	})
	return items
}

func (s *Store) Put(resource, ns, name string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(resource, ns, name)] = data
}

func (s *Store) PutObject(resource, ns, name string, obj map[string]interface{}) {
	s.Put(resource, ns, name, marshalObject(obj))
}

func (s *Store) Delete(resource, ns, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.key(resource, ns, name)
	if _, ok := s.data[key]; ok {
		delete(s.data, key)
		delete(s.logs, ns+"/"+name)
		return true
	}
	return false
}

func main() {
	if err := initData(); err != nil {
		log.Fatal(err)
	}

	caCert, caKey := generateCA()
	serverCert, serverKey := generateCert(caCert, caKey, true)
	clientCert, clientKey := generateCert(caCert, caKey, false)

	pkiDir := "/tmp/pki"
	_ = os.MkdirAll(pkiDir, 0755)
	writePEM(pkiDir+"/ca.crt", "CERTIFICATE", caCert)
	writePEM(pkiDir+"/server.crt", "CERTIFICATE", serverCert)
	writePEM(pkiDir+"/server.key", "EC PRIVATE KEY", marshalECKey(serverKey))
	writePEM(pkiDir+"/client.crt", "CERTIFICATE", clientCert)
	writePEM(pkiDir+"/client.key", "EC PRIVATE KEY", marshalECKey(clientKey))
	writeKubeconfig(caCert, clientCert, marshalECKey(clientKey))

	tlsCert, _ := tls.X509KeyPair(
		pemEncode("CERTIFICATE", serverCert),
		pemEncode("EC PRIVATE KEY", marshalECKey(serverKey)),
	)
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(pemEncode("CERTIFICATE", caCert))

	server := &http.Server{
		Addr:    "127.0.0.1:6443",
		Handler: http.HandlerFunc(router),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			ClientCAs:    caPool,
			ClientAuth:   tls.VerifyClientCertIfGiven,
		},
	}

	log.Println("Mock Kubernetes API server listening on https://127.0.0.1:6443")
	log.Fatal(server.ListenAndServeTLS("", ""))
}

func initData() error {
	seedPath := os.Getenv("KUBE_MOCK_SEED")
	if seedPath == "" {
		seedPath = "/etc/kubernetes/mock-seed.json"
	}
	raw, err := os.ReadFile(seedPath)
	if err != nil {
		return fmt.Errorf("read seed data %s: %w", seedPath, err)
	}
	var seed SeedData
	if err := json.Unmarshal(raw, &seed); err != nil {
		return fmt.Errorf("parse seed data %s: %w", seedPath, err)
	}

	store.mu.Lock()
	store.data = make(map[string][]byte)
	store.logs = make(map[string]string)
	for k, v := range seed.Logs {
		store.logs[k] = v
	}
	store.mu.Unlock()

	maxUID := 0
	maxRV := 0
	for _, rawObj := range seed.Resources {
		var obj map[string]interface{}
		if err := json.Unmarshal(rawObj, &obj); err != nil {
			return fmt.Errorf("parse seed resource: %w", err)
		}
		resource, ns, name, err := resourceKeyForObject(obj)
		if err != nil {
			return err
		}
		store.PutObject(resource, ns, name, obj)
		maxUID = max(maxUID, numericTail(strMap(nested(obj, "metadata"), "uid")))
		maxRV = max(maxRV, atoiDefault(strMap(nested(obj, "metadata"), "resourceVersion"), 0))
	}

	primeNodeRuntime()
	primeCounters(maxUID, maxRV)
	reconcileAllControllers()
	return nil
}

func primeNodeRuntime() {
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()

	nodes := make([]NodeRuntime, 0)
	for _, item := range store.ListAll("nodes") {
		var obj map[string]interface{}
		_ = json.Unmarshal(item, &obj)
		meta := nested(obj, "metadata")
		spec := nested(obj, "spec")
		status := nested(obj, "status")
		labels := nested(meta, "labels")
		hostIP := ""
		if addrs, ok := status["addresses"].([]interface{}); ok {
			for _, a := range addrs {
				am, _ := a.(map[string]interface{})
				if strMap(am, "type") == "InternalIP" {
					hostIP = strMap(am, "address")
					break
				}
			}
		}
		worker := true
		if _, ok := labels["node-role.kubernetes.io/control-plane"]; ok {
			worker = false
		}
		nodes = append(nodes, NodeRuntime{
			Name:      strMap(meta, "name"),
			HostIP:    hostIP,
			PodCIDR:   strMap(spec, "podCIDR"),
			Worker:    worker,
			NextOctet: 10,
		})
	}

	for _, item := range store.ListAll("pods") {
		var obj map[string]interface{}
		_ = json.Unmarshal(item, &obj)
		status := nested(obj, "status")
		ip := strMap(status, "podIP")
		for i := range nodes {
			if ipInCIDR(ip, nodes[i].PodCIDR) {
				octet := lastIPv4Octet(ip)
				if octet >= nodes[i].NextOctet {
					nodes[i].NextOctet = octet + 1
				}
			}
		}
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	runtimeState.nodes = nodes
	runtimeState.nextNodeIndex = 0
}

func primeCounters(maxUID, maxRV int) {
	uidCounter = maxUID + 1
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	if maxRV >= runtimeState.nextRV {
		runtimeState.nextRV = maxRV + 1
	}
}

func reconcileAllControllers() {
	for _, item := range store.ListAll("replicasets") {
		var obj map[string]interface{}
		_ = json.Unmarshal(item, &obj)
		meta := nested(obj, "metadata")
		reconcileReplicaSet(strMap(meta, "namespace"), strMap(meta, "name"))
	}
	for _, item := range store.ListAll("deployments") {
		var obj map[string]interface{}
		_ = json.Unmarshal(item, &obj)
		meta := nested(obj, "metadata")
		refreshDeploymentStatus(strMap(meta, "namespace"), strMap(meta, "name"))
	}
}

func router(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimSuffix(r.URL.Path, "/")

	switch path {
	case "/api":
		_, _ = w.Write([]byte(discoveryAPI))
		return
	case "/api/v1":
		_, _ = w.Write([]byte(discoveryV1))
		return
	case "/apis":
		_, _ = w.Write([]byte(discoveryAPIs))
		return
	case "/apis/apps", "/apis/apps/v1":
		_, _ = w.Write([]byte(discoveryAppsV1))
		return
	case "/apis/networking.k8s.io", "/apis/networking.k8s.io/v1":
		_, _ = w.Write([]byte(discoveryNetworkingV1))
		return
	case "/apis/gateway.networking.k8s.io", "/apis/gateway.networking.k8s.io/v1":
		_, _ = w.Write([]byte(discoveryGatewayV1))
		return
	case "/apis/argoproj.io", "/apis/argoproj.io/v1alpha1":
		_, _ = w.Write([]byte(discoveryArgoprojV1alpha1))
		return
	case "/api/v1/nodes":
		writeListOrTable(w, r, "nodes", store.ListAll("nodes"), "v1")
		return
	case "/api/v1/namespaces":
		if r.Method == http.MethodPost {
			handleCreate(w, r, "namespaces", "")
			return
		}
		writeList(w, "v1", "NamespaceList", store.List("namespaces", ""))
		return
	case "/openapi/v3":
		_, _ = w.Write([]byte(openAPIV3Index))
		return
	case "/openapi/v3/api/v1":
		_, _ = w.Write([]byte(openAPIV3CoreV1))
		return
	case "/openapi/v3/apis/apps/v1":
		_, _ = w.Write([]byte(openAPIV3AppsV1))
		return
	case "/openapi/v3/apis/networking.k8s.io/v1":
		_, _ = w.Write([]byte(openAPIV3NetworkingV1))
		return
	case "/openapi/v3/apis/gateway.networking.k8s.io/v1":
		_, _ = w.Write([]byte(openAPIV3GatewayV1))
		return
	case "/openapi/v3/apis/argoproj.io/v1alpha1":
		_, _ = w.Write([]byte(openAPIV3ArgoprojV1alpha1))
		return
	case "/openapi/v2":
		_, _ = w.Write([]byte(openAPIV2))
		return
	case "/version":
		_, _ = w.Write([]byte(versionJSON))
		return
	}

	if strings.HasPrefix(path, "/openapi") {
		http.NotFound(w, r)
		return
	}

	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")

	if len(parts) == 3 && parts[0] == "api" && parts[1] == "v1" && parts[2] != "namespaces" {
		resource := parts[2]
		writeListOrTable(w, r, resource, store.ListAll(resource), apiVersionForResource(resource))
		return
	}

	if len(parts) == 4 && parts[0] == "apis" && parts[3] != "namespaces" {
		resource := parts[3]
		writeListOrTable(w, r, resource, store.ListAll(resource), apiVersionForResource(resource))
		return
	}

	if len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && (parts[2] == "nodes" || parts[2] == "namespaces") {
		ns := ""
		if parts[2] == "namespaces" {
			ns = ""
		}
		handleSingleResource(w, r, parts[2], ns, parts[3])
		return
	}

	if len(parts) >= 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "namespaces" {
		ns := parts[3]
		resource := parts[4]
		if len(parts) == 5 {
			if r.Method == http.MethodPost {
				handleCreate(w, r, resource, ns)
			} else {
				writeListOrTable(w, r, resource, store.List(resource, ns), apiVersionForResource(resource))
			}
			return
		}
		if len(parts) == 6 {
			handleSingleResource(w, r, resource, ns, parts[5])
			return
		}
		if len(parts) == 7 && parts[6] == "log" {
			handleLogs(w, ns, parts[5])
			return
		}
	}

	if len(parts) >= 6 && parts[0] == "apis" && parts[3] == "namespaces" {
		ns := parts[4]
		resource := parts[5]
		if len(parts) == 6 {
			if r.Method == http.MethodPost {
				handleCreate(w, r, resource, ns)
			} else {
				writeListOrTable(w, r, resource, store.List(resource, ns), apiVersionForResource(resource))
			}
			return
		}
		if len(parts) == 7 {
			handleSingleResource(w, r, resource, ns, parts[6])
			return
		}
		if len(parts) == 8 && parts[7] == "scale" {
			handleScale(w, r, resource, ns, parts[6])
			return
		}
	}

	http.NotFound(w, r)
}

func handleSingleResource(w http.ResponseWriter, r *http.Request, resource, ns, name string) {
	switch r.Method {
	case http.MethodGet:
		data, ok := store.Get(resource, ns, name)
		if !ok {
			writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
			return
		}
		_, _ = w.Write(data)
	case http.MethodPut:
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
		enrichObject(resource, ns, name, obj)
		store.PutObject(resource, ns, name, obj)
		afterWrite(resource, ns, name)
		_, _ = w.Write(marshalObject(obj))
	case http.MethodPatch:
		current, ok := store.GetObject(resource, ns, name)
		if !ok {
			writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
			return
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			writeStatus(w, 400, "empty body")
			return
		}
		var patch map[string]interface{}
		if err := json.Unmarshal(body, &patch); err != nil {
			writeStatus(w, 400, "invalid JSON")
			return
		}
		merged := mergeObjects(current, patch)
		enrichObject(resource, ns, name, merged)
		store.PutObject(resource, ns, name, merged)
		afterWrite(resource, ns, name)
		_, _ = w.Write(marshalObject(merged))
	case http.MethodDelete:
		handleDelete(w, resource, ns, name)
	default:
		writeStatus(w, 405, "method not allowed")
	}
}

func handleDelete(w http.ResponseWriter, resource, ns, name string) {
	if resource == "pods" {
		pod, ok := store.GetObject(resource, ns, name)
		if !ok {
			writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
			return
		}
		ownerName, ownerKind := controllerOwner(pod)
		_ = store.Delete(resource, ns, name)
		if ownerKind == "ReplicaSet" {
			reconcileReplicaSet(ns, ownerName)
		}
		writeSuccessStatus(w, fmt.Sprintf("%s %q deleted", resource, name))
		return
	}
	if store.Delete(resource, ns, name) {
		writeSuccessStatus(w, fmt.Sprintf("%s %q deleted", resource, name))
		return
	}
	writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
}

func handleCreate(w http.ResponseWriter, r *http.Request, resource, ns string) {
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
	meta := nested(obj, "metadata")
	name := strMap(meta, "name")
	if name == "" {
		writeStatus(w, 400, "metadata.name required")
		return
	}
	enrichObject(resource, ns, name, obj)
	if resource == "pods" {
		assignPodRuntime(obj)
	}
	store.PutObject(resource, ns, name, obj)
	afterWrite(resource, ns, name)
	w.WriteHeader(201)
	_, _ = w.Write(marshalObject(obj))
}

func handleScale(w http.ResponseWriter, r *http.Request, resource, ns, name string) {
	if resource != "deployments" && resource != "replicasets" && resource != "statefulsets" {
		writeStatus(w, 404, "scale not supported")
		return
	}
	obj, ok := store.GetObject(resource, ns, name)
	if !ok {
		writeStatus(w, 404, fmt.Sprintf("%s %q not found", resource, name))
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeScale(w, resource, obj)
	case http.MethodPut, http.MethodPatch:
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			writeStatus(w, 400, "empty body")
			return
		}
		var scale map[string]interface{}
		if err := json.Unmarshal(body, &scale); err != nil {
			writeStatus(w, 400, "invalid JSON")
			return
		}
		replicas := intFromNested(scale, "spec", "replicas")
		spec := nested(obj, "spec")
		spec["replicas"] = replicas
		obj["spec"] = spec
		enrichObject(resource, ns, name, obj)
		store.PutObject(resource, ns, name, obj)
		afterWrite(resource, ns, name)
		updated, _ := store.GetObject(resource, ns, name)
		writeScale(w, resource, updated)
	default:
		writeStatus(w, 405, "method not allowed")
	}
}

func writeScale(w http.ResponseWriter, resource string, obj map[string]interface{}) {
	meta := nested(obj, "metadata")
	spec := nested(obj, "spec")
	status := nested(obj, "status")
	resp := map[string]interface{}{
		"apiVersion": "autoscaling/v1",
		"kind":       "Scale",
		"metadata": map[string]interface{}{
			"name":              strMap(meta, "name"),
			"namespace":         strMap(meta, "namespace"),
			"resourceVersion":   strMap(meta, "resourceVersion"),
			"creationTimestamp": strMap(meta, "creationTimestamp"),
		},
		"spec": map[string]interface{}{
			"replicas": intFromValue(spec["replicas"]),
		},
		"status": map[string]interface{}{
			"replicas": intFromValue(status["replicas"]),
			"selector": labelSelectorString(selectorForResource(resource, obj)),
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func handleLogs(w http.ResponseWriter, ns, podName string) {
	w.Header().Set("Content-Type", "text/plain")
	store.mu.RLock()
	defer store.mu.RUnlock()
	if logText, ok := store.logs[ns+"/"+podName]; ok {
		_, _ = w.Write([]byte(logText))
		return
	}
	_, _ = w.Write([]byte("No logs available for this pod.\n"))
}

func afterWrite(resource, ns, name string) {
	switch resource {
	case "deployments":
		reconcileDeployment(ns, name)
	case "replicasets":
		reconcileReplicaSet(ns, name)
	case "statefulsets":
		refreshStatefulSetStatus(ns, name)
	}
}

func reconcileDeployment(ns, name string) {
	deploy, ok := store.GetObject("deployments", ns, name)
	if !ok {
		return
	}
	rsName := deploymentReplicaSetName(deploy)
	rs, ok := store.GetObject("replicasets", ns, rsName)
	if !ok {
		rs = newReplicaSetFromDeployment(deploy, rsName)
	}
	rsSpec := nested(rs, "spec")
	rsSpec["replicas"] = intFromNested(deploy, "spec", "replicas")
	rs["spec"] = rsSpec
	enrichObject("replicasets", ns, rsName, rs)
	store.PutObject("replicasets", ns, rsName, rs)
	reconcileReplicaSet(ns, rsName)
	refreshDeploymentStatus(ns, name)
}

func reconcileReplicaSet(ns, name string) {
	rs, ok := store.GetObject("replicasets", ns, name)
	if !ok {
		return
	}
	spec := nested(rs, "spec")
	template := nested(spec, "template")
	templateMeta := nested(template, "metadata")
	templateLabels := nested(templateMeta, "labels")
	targetReplicas := intFromValue(spec["replicas"])
	pods := podsOwnedByReplicaSet(ns, name)

	for len(pods) < targetReplicas {
		pod := newPodFromTemplate(ns, name, template, templateLabels)
		podName := strMap(nested(pod, "metadata"), "name")
		store.PutObject("pods", ns, podName, pod)
		pods = append(pods, pod)
	}

	if len(pods) > targetReplicas {
		sort.Slice(pods, func(i, j int) bool {
			return strMap(nested(pods[i], "metadata"), "name") > strMap(nested(pods[j], "metadata"), "name")
		})
		for _, pod := range pods[targetReplicas:] {
			_ = store.Delete("pods", ns, strMap(nested(pod, "metadata"), "name"))
		}
		pods = pods[:targetReplicas]
	}

	ready := 0
	for _, pod := range pods {
		if strMap(nested(pod, "status"), "phase") == "Running" {
			ready++
		}
	}
	status := nested(rs, "status")
	status["replicas"] = len(pods)
	status["readyReplicas"] = ready
	status["availableReplicas"] = ready
	rs["status"] = status
	enrichObject("replicasets", ns, name, rs)
	store.PutObject("replicasets", ns, name, rs)

	if depName := deploymentNameForReplicaSet(rs); depName != "" {
		refreshDeploymentStatus(ns, depName)
	}
}

func refreshDeploymentStatus(ns, name string) {
	deploy, ok := store.GetObject("deployments", ns, name)
	if !ok {
		return
	}
	pods := podsMatchingSelector(ns, selectorForResource("deployments", deploy))
	ready := 0
	for _, pod := range pods {
		if strMap(nested(pod, "status"), "phase") == "Running" {
			ready++
		}
	}
	status := nested(deploy, "status")
	status["replicas"] = len(pods)
	status["readyReplicas"] = ready
	status["availableReplicas"] = ready
	status["updatedReplicas"] = len(pods)
	deploy["status"] = status
	enrichObject("deployments", ns, name, deploy)
	store.PutObject("deployments", ns, name, deploy)
}

func refreshStatefulSetStatus(ns, name string) {
	ss, ok := store.GetObject("statefulsets", ns, name)
	if !ok {
		return
	}
	pods := podsMatchingSelector(ns, selectorForResource("statefulsets", ss))
	status := nested(ss, "status")
	status["replicas"] = len(pods)
	status["readyReplicas"] = len(pods)
	status["currentReplicas"] = len(pods)
	ss["status"] = status
	enrichObject("statefulsets", ns, name, ss)
	store.PutObject("statefulsets", ns, name, ss)
}

func newReplicaSetFromDeployment(deploy map[string]interface{}, rsName string) map[string]interface{} {
	meta := nested(deploy, "metadata")
	spec := nested(deploy, "spec")
	template := deepCopyMap(nested(spec, "template"))
	templateMeta := nested(template, "metadata")
	labels := nested(templateMeta, "labels")
	hash := strings.TrimPrefix(rsName, strMap(meta, "name")+"-")
	if labels["pod-template-hash"] == nil {
		labels["pod-template-hash"] = hash
	}
	templateMeta["labels"] = labels
	template["metadata"] = templateMeta
	return map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "ReplicaSet",
		"metadata": map[string]interface{}{
			"name":              rsName,
			"namespace":         strMap(meta, "namespace"),
			"labels":            map[string]interface{}{"app": nested(templateMeta, "labels")["app"], "pod-template-hash": hash},
			"ownerReferences":   []interface{}{map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "name": strMap(meta, "name")}},
			"creationTimestamp": strMap(meta, "creationTimestamp"),
		},
		"spec": map[string]interface{}{
			"replicas": intFromValue(spec["replicas"]),
			"selector": nested(spec, "selector"),
			"template": template,
		},
		"status": map[string]interface{}{},
	}
}

func newPodFromTemplate(ns, rsName string, template, templateLabels map[string]interface{}) map[string]interface{} {
	baseName := strings.TrimSuffix(rsName, "-"+strMap(templateLabels, "pod-template-hash"))
	if baseName == rsName {
		baseName = rsName
	}
	podName := fmt.Sprintf("%s-%s", rsName, nextPodSuffix())
	spec := deepCopyMap(nested(template, "spec"))
	labels := deepCopyMap(templateLabels)
	if labels["pod-template-hash"] == nil {
		labels["pod-template-hash"] = strings.TrimPrefix(rsName, baseName+"-")
	}
	nodeName, hostIP, podIP := allocatePodNetwork()
	now := time.Now().UTC().Format(time.RFC3339)
	containers := interfaceSlice(spec["containers"])
	containerStatuses := make([]interface{}, 0, len(containers))
	for _, c := range containers {
		cm, _ := c.(map[string]interface{})
		containerStatuses = append(containerStatuses, map[string]interface{}{
			"name":         strMap(cm, "name"),
			"ready":        true,
			"restartCount": 0,
			"image":        strMap(cm, "image"),
			"state":        map[string]interface{}{"running": map[string]interface{}{"startedAt": now}},
		})
	}
	spec["nodeName"] = nodeName
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name":              podName,
			"namespace":         ns,
			"labels":            labels,
			"creationTimestamp": now,
			"ownerReferences":   []interface{}{map[string]interface{}{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": rsName}},
		},
		"spec": spec,
		"status": map[string]interface{}{
			"phase":  "Running",
			"podIP":  podIP,
			"hostIP": hostIP,
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
			},
			"containerStatuses": containerStatuses,
		},
	}
}

func assignPodRuntime(obj map[string]interface{}) {
	spec := nested(obj, "spec")
	status := nested(obj, "status")
	if strMap(spec, "nodeName") == "" || strMap(status, "podIP") == "" || strMap(status, "hostIP") == "" {
		nodeName, hostIP, podIP := allocatePodNetwork()
		if strMap(spec, "nodeName") == "" {
			spec["nodeName"] = nodeName
		}
		if strMap(status, "podIP") == "" {
			status["podIP"] = podIP
		}
		if strMap(status, "hostIP") == "" {
			status["hostIP"] = hostIP
		}
	}
	if status["phase"] == nil {
		status["phase"] = "Running"
	}
	obj["spec"] = spec
	obj["status"] = status
}

func allocatePodNetwork() (string, string, string) {
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	if len(runtimeState.nodes) == 0 {
		return "", "", ""
	}
	candidates := make([]int, 0)
	for i, n := range runtimeState.nodes {
		if n.Worker {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		for i := range runtimeState.nodes {
			candidates = append(candidates, i)
		}
	}
	idx := candidates[runtimeState.nextNodeIndex%len(candidates)]
	runtimeState.nextNodeIndex++
	node := &runtimeState.nodes[idx]
	base := cidrBase(node.PodCIDR)
	ip := fmt.Sprintf("%s.%d", base, node.NextOctet)
	node.NextOctet++
	return node.Name, node.HostIP, ip
}

func podsOwnedByReplicaSet(ns, rsName string) []map[string]interface{} {
	pods := make([]map[string]interface{}, 0)
	for _, item := range store.List("pods", ns) {
		var pod map[string]interface{}
		_ = json.Unmarshal(item, &pod)
		ownerName, ownerKind := controllerOwner(pod)
		if ownerKind == "ReplicaSet" && ownerName == rsName {
			pods = append(pods, pod)
		}
	}
	return pods
}

func podsMatchingSelector(ns string, selector map[string]interface{}) []map[string]interface{} {
	items := store.List("pods", ns)
	result := make([]map[string]interface{}, 0)
	for _, item := range items {
		var pod map[string]interface{}
		_ = json.Unmarshal(item, &pod)
		if labelsMatch(nested(nested(pod, "metadata"), "labels"), selector) {
			result = append(result, pod)
		}
	}
	return result
}

func deploymentReplicaSetName(deploy map[string]interface{}) string {
	meta := nested(deploy, "metadata")
	templateLabels := nested(nested(nested(deploy, "spec"), "template"), "metadata")
	labels := nested(templateLabels, "labels")
	if hash := strMap(labels, "pod-template-hash"); hash != "" {
		return strMap(meta, "name") + "-" + hash
	}
	return strMap(meta, "name") + "-" + shortStableName(strMap(meta, "name"))
}

func deploymentNameForReplicaSet(rs map[string]interface{}) string {
	if owners, ok := nested(rs, "metadata")["ownerReferences"].([]interface{}); ok {
		for _, owner := range owners {
			om, _ := owner.(map[string]interface{})
			if strMap(om, "kind") == "Deployment" {
				return strMap(om, "name")
			}
		}
	}
	return ""
}

func controllerOwner(obj map[string]interface{}) (string, string) {
	meta := nested(obj, "metadata")
	if owners, ok := meta["ownerReferences"].([]interface{}); ok {
		for _, owner := range owners {
			om, _ := owner.(map[string]interface{})
			return strMap(om, "name"), strMap(om, "kind")
		}
	}
	return "", ""
}

func writeListOrTable(w http.ResponseWriter, r *http.Request, resource string, items []json.RawMessage, apiVersion string) {
	if wantsTable(r) {
		if cols, _ := tableColumnsForResource(resource); cols != nil {
			writeTableList(w, resource, items)
			return
		}
	}
	writeList(w, apiVersion, kindForResource(resource)+"List", items)
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
	_ = json.NewEncoder(w).Encode(resp)
}

func writeTableList(w http.ResponseWriter, resource string, items []json.RawMessage) {
	columns, rowFn := tableColumnsForResource(resource)
	rows := make([]interface{}, 0, len(items))
	for _, item := range items {
		var obj map[string]interface{}
		_ = json.Unmarshal(item, &obj)
		rows = append(rows, map[string]interface{}{
			"cells":  rowFn(obj),
			"object": map[string]interface{}{"apiVersion": obj["apiVersion"], "kind": obj["kind"], "metadata": obj["metadata"]},
		})
	}
	resp := map[string]interface{}{
		"apiVersion":        "meta.k8s.io/v1",
		"kind":              "Table",
		"metadata":          map[string]interface{}{"resourceVersion": "1000"},
		"columnDefinitions": columns,
		"rows":              rows,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func tableColumnsForResource(resource string) ([]interface{}, func(map[string]interface{}) []interface{}) {
	col := func(name, typ string, priority int) interface{} {
		return map[string]interface{}{"name": name, "type": typ, "priority": priority}
	}

	switch resource {
	case "nodes":
		return []interface{}{
				col("Name", "string", 0), col("Status", "string", 0), col("Roles", "string", 0),
				col("Age", "string", 0), col("Version", "string", 0), col("Internal-IP", "string", 1),
				col("OS-Image", "string", 1), col("Kernel-Version", "string", 1), col("Container-Runtime", "string", 1),
			}, func(o map[string]interface{}) []interface{} {
				status := nested(o, "status")
				nodeInfo := nested(status, "nodeInfo")
				meta := nested(o, "metadata")
				labels := nested(meta, "labels")
				role := "<none>"
				if _, ok := labels["node-role.kubernetes.io/control-plane"]; ok {
					role = "control-plane"
				}
				ready := "NotReady"
				for _, c := range interfaceSlice(status["conditions"]) {
					cm, _ := c.(map[string]interface{})
					if strMap(cm, "type") == "Ready" && strMap(cm, "status") == "True" {
						ready = "Ready"
					}
				}
				return []interface{}{
					strMap(meta, "name"), ready, role, strMap(meta, "creationTimestamp"),
					strMap(nodeInfo, "kubeletVersion"), nodeInternalIP(o), strMap(nodeInfo, "osImage"),
					strMap(nodeInfo, "kernelVersion"), strMap(nodeInfo, "containerRuntimeVersion"),
				}
			}
	case "pods":
		return []interface{}{
				col("Name", "string", 0), col("Ready", "string", 0), col("Status", "string", 0),
				col("Restarts", "integer", 0), col("Age", "string", 0), col("IP", "string", 1), col("Node", "string", 1),
			}, func(o map[string]interface{}) []interface{} {
				status := nested(o, "status")
				spec := nested(o, "spec")
				meta := nested(o, "metadata")
				ready, total, restarts := podReadiness(status)
				return []interface{}{
					strMap(meta, "name"), fmt.Sprintf("%d/%d", ready, total), strMap(status, "phase"),
					restarts, strMap(meta, "creationTimestamp"), strMap(status, "podIP"), strMap(spec, "nodeName"),
				}
			}
	case "services":
		return []interface{}{
				col("Name", "string", 0), col("Type", "string", 0), col("Cluster-IP", "string", 0),
				col("Ports", "string", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				spec := nested(o, "spec")
				return []interface{}{
					strMap(meta, "name"), strMap(spec, "type"), strMap(spec, "clusterIP"), portSummary(spec), strMap(meta, "creationTimestamp"),
				}
			}
	case "deployments", "replicasets", "statefulsets":
		return []interface{}{
				col("Name", "string", 0), col("Ready", "string", 0), col("Up-to-date", "integer", 0),
				col("Available", "integer", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				spec := nested(o, "spec")
				status := nested(o, "status")
				ready := fmt.Sprintf("%d/%d", intFromValue(status["readyReplicas"]), intFromValue(spec["replicas"]))
				return []interface{}{
					strMap(meta, "name"), ready, intFromValue(status["updatedReplicas"]), intFromValue(status["availableReplicas"]), strMap(meta, "creationTimestamp"),
				}
			}
	case "daemonsets":
		return []interface{}{
				col("Name", "string", 0), col("Desired", "integer", 0), col("Current", "integer", 0),
				col("Ready", "integer", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				status := nested(o, "status")
				return []interface{}{
					strMap(meta, "name"), intFromValue(status["desiredNumberScheduled"]), intFromValue(status["currentNumberScheduled"]),
					intFromValue(status["numberReady"]), strMap(meta, "creationTimestamp"),
				}
			}
	case "networkpolicies":
		return []interface{}{
				col("Name", "string", 0), col("Pod-Selector", "string", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				spec := nested(o, "spec")
				return []interface{}{
					strMap(meta, "name"), selectorString(nested(spec, "podSelector")), strMap(meta, "creationTimestamp"),
				}
			}
	case "gateways":
		return []interface{}{
				col("Name", "string", 0), col("Class", "string", 0), col("Address", "string", 0),
				col("Programmed", "string", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				spec := nested(o, "spec")
				status := nested(o, "status")
				address := ""
				if addrs, ok := status["addresses"].([]interface{}); ok && len(addrs) > 0 {
					am, _ := addrs[0].(map[string]interface{})
					address = strMap(am, "value")
				}
				programmed := "Unknown"
				for _, cond := range interfaceSlice(status["conditions"]) {
					cm, _ := cond.(map[string]interface{})
					if strMap(cm, "type") == "Programmed" {
						programmed = strMap(cm, "status")
					}
				}
				return []interface{}{
					strMap(meta, "name"), strMap(spec, "gatewayClassName"), address, programmed, strMap(meta, "creationTimestamp"),
				}
			}
	case "httproutes":
		return []interface{}{
				col("Name", "string", 0), col("Hostnames", "string", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				spec := nested(o, "spec")
				parts := make([]string, 0)
				for _, h := range interfaceSlice(spec["hostnames"]) {
					if hs, ok := h.(string); ok {
						parts = append(parts, hs)
					}
				}
				return []interface{}{
					strMap(meta, "name"), strings.Join(parts, ","), strMap(meta, "creationTimestamp"),
				}
			}
	case "applications":
		return []interface{}{
				col("Name", "string", 0), col("Sync Status", "string", 0), col("Health Status", "string", 0),
				col("Project", "string", 0), col("Age", "string", 0),
			}, func(o map[string]interface{}) []interface{} {
				meta := nested(o, "metadata")
				spec := nested(o, "spec")
				status := nested(o, "status")
				return []interface{}{
					strMap(meta, "name"), strMap(nested(status, "sync"), "status"), strMap(nested(status, "health"), "status"),
					strMap(spec, "project"), strMap(meta, "creationTimestamp"),
				}
			}
	default:
		return nil, nil
	}
}

func enrichObject(resource, ns, name string, obj map[string]interface{}) {
	meta := nested(obj, "metadata")
	if resource == "namespaces" {
		ns = ""
	}
	if ns != "" {
		meta["namespace"] = ns
	}
	meta["name"] = name
	if strMap(meta, "creationTimestamp") == "" {
		meta["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	}
	if strMap(meta, "uid") == "" {
		meta["uid"] = uid()
	}
	meta["resourceVersion"] = nextResourceVersion()
	obj["metadata"] = meta
	if obj["apiVersion"] == nil {
		obj["apiVersion"] = apiVersionForResource(resource)
	}
	if obj["kind"] == nil {
		obj["kind"] = kindForResource(resource)
	}
}

func mergeObjects(dst, patch map[string]interface{}) map[string]interface{} {
	result := deepCopyMap(dst)
	for k, v := range patch {
		existing, ok := result[k]
		if ok {
			em, eok := existing.(map[string]interface{})
			pm, pok := v.(map[string]interface{})
			if eok && pok {
				result[k] = mergeObjects(em, pm)
				continue
			}
		}
		result[k] = v
	}
	return result
}

func resourceKeyForObject(obj map[string]interface{}) (string, string, string, error) {
	resource := resourceForKind(strMap(obj, "kind"))
	if resource == "" {
		return "", "", "", fmt.Errorf("unsupported kind %q in seed data", strMap(obj, "kind"))
	}
	meta := nested(obj, "metadata")
	name := strMap(meta, "name")
	if name == "" {
		return "", "", "", fmt.Errorf("seed object missing metadata.name")
	}
	ns := strMap(meta, "namespace")
	if resource == "nodes" || resource == "namespaces" {
		ns = ""
	}
	return resource, ns, name, nil
}

func resourceForKind(kind string) string {
	switch kind {
	case "Namespace":
		return "namespaces"
	case "Node":
		return "nodes"
	case "Pod":
		return "pods"
	case "Service":
		return "services"
	case "ConfigMap":
		return "configmaps"
	case "Secret":
		return "secrets"
	case "Deployment":
		return "deployments"
	case "ReplicaSet":
		return "replicasets"
	case "StatefulSet":
		return "statefulsets"
	case "DaemonSet":
		return "daemonsets"
	case "NetworkPolicy":
		return "networkpolicies"
	case "Gateway":
		return "gateways"
	case "HTTPRoute":
		return "httproutes"
	case "Application":
		return "applications"
	default:
		return ""
	}
}

func apiVersionForResource(resource string) string {
	switch resource {
	case "deployments", "replicasets", "statefulsets", "daemonsets":
		return "apps/v1"
	case "networkpolicies":
		return "networking.k8s.io/v1"
	case "gateways", "httproutes":
		return "gateway.networking.k8s.io/v1"
	case "applications":
		return "argoproj.io/v1alpha1"
	default:
		return "v1"
	}
}

func kindForResource(resource string) string {
	switch resource {
	case "pods":
		return "Pod"
	case "services":
		return "Service"
	case "configmaps":
		return "ConfigMap"
	case "secrets":
		return "Secret"
	case "namespaces":
		return "Namespace"
	case "nodes":
		return "Node"
	case "deployments":
		return "Deployment"
	case "replicasets":
		return "ReplicaSet"
	case "statefulsets":
		return "StatefulSet"
	case "daemonsets":
		return "DaemonSet"
	case "networkpolicies":
		return "NetworkPolicy"
	case "gateways":
		return "Gateway"
	case "httproutes":
		return "HTTPRoute"
	case "applications":
		return "Application"
	default:
		return "Unknown"
	}
}

func selectorForResource(resource string, obj map[string]interface{}) map[string]interface{} {
	spec := nested(obj, "spec")
	switch resource {
	case "deployments", "replicasets", "statefulsets":
		return nested(nested(spec, "selector"), "matchLabels")
	default:
		return map[string]interface{}{}
	}
}

func labelsMatch(labels, selector map[string]interface{}) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func selectorString(selector map[string]interface{}) string {
	if len(selector) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(selector))
	for k := range selector {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, selector[k]))
	}
	return strings.Join(parts, ",")
}

func labelSelectorString(selector map[string]interface{}) string {
	return selectorString(selector)
}

func portSummary(spec map[string]interface{}) string {
	ports := interfaceSlice(spec["ports"])
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		pm, _ := p.(map[string]interface{})
		port := intFromValue(pm["port"])
		proto := strMap(pm, "protocol")
		if proto == "" {
			proto = "TCP"
		}
		parts = append(parts, fmt.Sprintf("%d/%s", port, proto))
	}
	return strings.Join(parts, ",")
}

func podReadiness(status map[string]interface{}) (int, int, int) {
	containers := interfaceSlice(status["containerStatuses"])
	total := len(containers)
	ready := 0
	restarts := 0
	for _, c := range containers {
		cm, _ := c.(map[string]interface{})
		if truthy(cm["ready"]) {
			ready++
		}
		restarts += intFromValue(cm["restartCount"])
	}
	return ready, total, restarts
}

func nodeInternalIP(node map[string]interface{}) string {
	status := nested(node, "status")
	for _, a := range interfaceSlice(status["addresses"]) {
		am, _ := a.(map[string]interface{})
		if strMap(am, "type") == "InternalIP" {
			return strMap(am, "address")
		}
	}
	return ""
}

func nextResourceVersion() string {
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	rv := runtimeState.nextRV
	runtimeState.nextRV++
	return strconv.Itoa(rv)
}

func nextPodSuffix() string {
	runtimeState.mu.Lock()
	defer runtimeState.mu.Unlock()
	s := fmt.Sprintf("%05d", runtimeState.nextSuffix)
	runtimeState.nextSuffix++
	return s
}

func cidrBase(cidr string) string {
	ip, _, err := net.ParseCIDR(cidr)
	if err != nil {
		return "10.244.255"
	}
	parts := strings.Split(ip.String(), ".")
	if len(parts) != 4 {
		return "10.244.255"
	}
	return strings.Join(parts[:3], ".")
}

func ipInCIDR(ip, cidr string) bool {
	parsedIP := net.ParseIP(ip)
	_, network, err := net.ParseCIDR(cidr)
	return err == nil && parsedIP != nil && network.Contains(parsedIP)
}

func lastIPv4Octet(ip string) int {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return 0
	}
	return atoiDefault(parts[3], 0)
}

func intFromNested(obj map[string]interface{}, path ...string) int {
	cur := interface{}(obj)
	for _, p := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return 0
		}
		cur = m[p]
	}
	return intFromValue(cur)
}

func intFromValue(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case string:
		return atoiDefault(n, 0)
	default:
		return 0
	}
}

func nested(obj map[string]interface{}, key string) map[string]interface{} {
	if obj == nil {
		return map[string]interface{}{}
	}
	if v, ok := obj[key].(map[string]interface{}); ok && v != nil {
		return v
	}
	return map[string]interface{}{}
}

func interfaceSlice(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return []interface{}{}
}

func strMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func strMust(raw json.RawMessage, path ...string) string {
	var obj map[string]interface{}
	_ = json.Unmarshal(raw, &obj)
	cur := interface{}(obj)
	for _, p := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = m[p]
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

func deepCopyMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return map[string]interface{}{}
	}
	b, _ := json.Marshal(src)
	var dst map[string]interface{}
	_ = json.Unmarshal(b, &dst)
	return dst
}

func marshalObject(obj map[string]interface{}) []byte {
	b, _ := json.Marshal(obj)
	return b
}

func truthy(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func atoiDefault(v string, fallback int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func numericTail(v string) int {
	digits := ""
	for i := len(v) - 1; i >= 0; i-- {
		if v[i] < '0' || v[i] > '9' {
			break
		}
		digits = string(v[i]) + digits
	}
	return atoiDefault(digits, 0)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func shortStableName(name string) string {
	sum := 0
	for _, r := range name {
		sum = (sum*33 + int(r)) % 1000000000
	}
	return fmt.Sprintf("%010d", sum)[:10]
}

func generateCA() (certDER []byte, key *ecdsa.PrivateKey) {
	key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"mock-kubernetes"}, CommonName: "mock-kubernetes-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certDER, _ = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	return
}

func generateCert(caDER []byte, caKey *ecdsa.PrivateKey, isServer bool) (certDER []byte, key *ecdsa.PrivateKey) {
	ca, _ := x509.ParseCertificate(caDER)
	key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	if isServer {
		tmpl.Subject = pkix.Name{Organization: []string{"mock-kubernetes"}, CommonName: "kube-apiserver"}
		tmpl.DNSNames = []string{"localhost", "kubernetes", "kubernetes.default", "kubernetes.default.svc"}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("10.96.0.1")}
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		tmpl.Subject = pkix.Name{Organization: []string{"system:masters"}, CommonName: "student"}
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	certDER, _ = x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	return
}

func marshalECKey(key *ecdsa.PrivateKey) []byte {
	b, _ := x509.MarshalECPrivateKey(key)
	return b
}

func pemEncode(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

func writePEM(path, typ string, der []byte) {
	f, _ := os.Create(path)
	defer f.Close()
	_ = pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func writeKubeconfig(caDER, clientCertDER, clientKeyDER []byte) {
	caPEM := pemEncode("CERTIFICATE", caDER)
	clientCertPEM := pemEncode("CERTIFICATE", clientCertDER)
	clientKeyPEM := pemEncode("EC PRIVATE KEY", clientKeyDER)

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: %s
    server: https://127.0.0.1:6443
  name: mock-cluster
contexts:
- context:
    cluster: mock-cluster
    namespace: default
    user: student
  name: mock
current-context: mock
users:
- name: student
  user:
    client-certificate-data: %s
    client-key-data: %s
`,
		base64.StdEncoding.EncodeToString(caPEM),
		base64.StdEncoding.EncodeToString(clientCertPEM),
		base64.StdEncoding.EncodeToString(clientKeyPEM),
	)

	_ = os.MkdirAll("/home/termuser/.kube", 0755)
	_ = os.WriteFile("/home/termuser/.kube/config", []byte(kubeconfig), 0600)
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
	_ = json.NewEncoder(w).Encode(resp)
}

func writeSuccessStatus(w http.ResponseWriter, msg string) {
	resp := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Status",
		"metadata":   map[string]interface{}{},
		"status":     "Success",
		"message":    msg,
		"code":       200,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

const versionJSON = `{"major":"1","minor":"34","gitVersion":"v1.34.0","platform":"linux/arm64"}`

const discoveryAPI = `{"kind":"APIVersions","versions":["v1"],"serverAddressByClientCIDRs":[{"clientCIDR":"0.0.0.0/0","serverAddress":"127.0.0.1:6443"}]}`

const discoveryV1 = `{
  "kind":"APIResourceList","groupVersion":"v1",
  "resources":[
    {"name":"namespaces","singularName":"namespace","namespaced":false,"kind":"Namespace","shortNames":["ns"],"verbs":["get","list","create","update","patch","delete"]},
    {"name":"nodes","singularName":"node","namespaced":false,"kind":"Node","shortNames":["no"],"verbs":["get","list"]},
		{"name":"pods","singularName":"pod","namespaced":true,"kind":"Pod","shortNames":["po"],"verbs":["get","list","create","update","patch","delete"]},
		{"name":"pods/log","singularName":"","namespaced":true,"kind":"Pod","verbs":["get"]},
		{"name":"services","singularName":"service","namespaced":true,"kind":"Service","shortNames":["svc"],"verbs":["get","list","create","update","patch","delete"]},
		{"name":"configmaps","singularName":"configmap","namespaced":true,"kind":"ConfigMap","shortNames":["cm"],"verbs":["get","list","create","update","patch","delete"]},
		{"name":"secrets","singularName":"secret","namespaced":true,"kind":"Secret","shortNames":["sec"],"verbs":["get","list","create","update","patch","delete"]}
  ]}`

const discoveryAPIs = `{
  "kind":"APIGroupList",
  "groups":[
    {"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}},
    {"name":"networking.k8s.io","versions":[{"groupVersion":"networking.k8s.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"networking.k8s.io/v1","version":"v1"}},
    {"name":"gateway.networking.k8s.io","versions":[{"groupVersion":"gateway.networking.k8s.io/v1","version":"v1"}],"preferredVersion":{"groupVersion":"gateway.networking.k8s.io/v1","version":"v1"}},
    {"name":"argoproj.io","versions":[{"groupVersion":"argoproj.io/v1alpha1","version":"v1alpha1"}],"preferredVersion":{"groupVersion":"argoproj.io/v1alpha1","version":"v1alpha1"}}
  ]}`

const discoveryAppsV1 = `{
  "kind":"APIResourceList","groupVersion":"apps/v1",
  "resources":[
		{"name":"deployments","singularName":"deployment","namespaced":true,"kind":"Deployment","shortNames":["deploy"],"verbs":["get","list","create","update","patch","delete"]},
    {"name":"deployments/scale","singularName":"","namespaced":true,"kind":"Scale","group":"autoscaling","version":"v1","verbs":["get","update","patch"]},
    {"name":"replicasets","singularName":"replicaset","namespaced":true,"kind":"ReplicaSet","shortNames":["rs"],"verbs":["get","list","create","update","patch","delete"]},
    {"name":"replicasets/scale","singularName":"","namespaced":true,"kind":"Scale","group":"autoscaling","version":"v1","verbs":["get","update","patch"]},
		{"name":"statefulsets","singularName":"statefulset","namespaced":true,"kind":"StatefulSet","shortNames":["sts"],"verbs":["get","list","create","update","patch","delete"]},
    {"name":"statefulsets/scale","singularName":"","namespaced":true,"kind":"Scale","group":"autoscaling","version":"v1","verbs":["get","update","patch"]},
    {"name":"daemonsets","singularName":"daemonset","namespaced":true,"kind":"DaemonSet","shortNames":["ds"],"verbs":["get","list","create","update","patch","delete"]}
  ]}`

const discoveryNetworkingV1 = `{
  "kind":"APIResourceList","groupVersion":"networking.k8s.io/v1",
  "resources":[
    {"name":"networkpolicies","singularName":"networkpolicy","namespaced":true,"kind":"NetworkPolicy","shortNames":["netpol"],"verbs":["get","list","create","update","patch","delete"]}
  ]}`

const discoveryGatewayV1 = `{
  "kind":"APIResourceList","groupVersion":"gateway.networking.k8s.io/v1",
  "resources":[
    {"name":"gateways","singularName":"gateway","namespaced":true,"kind":"Gateway","shortNames":["gw"],"verbs":["get","list","create","update","patch","delete"]},
    {"name":"httproutes","singularName":"httproute","namespaced":true,"kind":"HTTPRoute","shortNames":["hr"],"verbs":["get","list","create","update","patch","delete"]}
  ]}`

const discoveryArgoprojV1alpha1 = `{
  "kind":"APIResourceList","groupVersion":"argoproj.io/v1alpha1",
  "resources":[
    {"name":"applications","singularName":"application","namespaced":true,"kind":"Application","shortNames":["app","apps"],"verbs":["get","list","create","update","patch","delete"]}
  ]}`

const openAPIV2 = `{
  "swagger": "2.0",
  "info": {
    "title": "mock-kubernetes",
    "version": "v1.34.0"
  },
  "paths": {},
  "definitions": {
    "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
      "type": "object",
      "additionalProperties": true
    },
    "io.k8s.api.core.v1.Pod": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"","version":"v1","kind":"Pod"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.core.v1.Service": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"","version":"v1","kind":"Service"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.core.v1.ConfigMap": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"","version":"v1","kind":"ConfigMap"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "data": {"type":"object","additionalProperties":{"type":"string"}}
      }
    },
    "io.k8s.api.core.v1.Secret": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"","version":"v1","kind":"Secret"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "data": {"type":"object","additionalProperties":{"type":"string"}},
        "type": {"type":"string"}
      }
    },
    "io.k8s.api.core.v1.Namespace": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"","version":"v1","kind":"Namespace"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.core.v1.Node": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"","version":"v1","kind":"Node"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.apps.v1.Deployment": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"apps","version":"v1","kind":"Deployment"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.apps.v1.ReplicaSet": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"apps","version":"v1","kind":"ReplicaSet"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.apps.v1.StatefulSet": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"apps","version":"v1","kind":"StatefulSet"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.apps.v1.DaemonSet": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"apps","version":"v1","kind":"DaemonSet"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.networking.v1.NetworkPolicy": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"networking.k8s.io","version":"v1","kind":"NetworkPolicy"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.gateway.networking.v1.Gateway": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"gateway.networking.k8s.io","version":"v1","kind":"Gateway"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.k8s.api.gateway.networking.v1.HTTPRoute": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"gateway.networking.k8s.io","version":"v1","kind":"HTTPRoute"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    },
    "io.argoproj.v1alpha1.Application": {
      "type": "object",
      "x-kubernetes-group-version-kind": [{"group":"argoproj.io","version":"v1alpha1","kind":"Application"}],
      "properties": {
        "apiVersion": {"type":"string"},
        "kind": {"type":"string"},
        "metadata": {"$ref":"#/definitions/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},
        "spec": {"type":"object","additionalProperties": true},
        "status": {"type":"object","additionalProperties": true}
      }
    }
  }
}`

const openAPIV3Index = `{
  "paths": {
    "api/v1": {"serverRelativeURL": "/openapi/v3/api/v1"},
    "apis/apps/v1": {"serverRelativeURL": "/openapi/v3/apis/apps/v1"},
    "apis/networking.k8s.io/v1": {"serverRelativeURL": "/openapi/v3/apis/networking.k8s.io/v1"},
    "apis/gateway.networking.k8s.io/v1": {"serverRelativeURL": "/openapi/v3/apis/gateway.networking.k8s.io/v1"},
    "apis/argoproj.io/v1alpha1": {"serverRelativeURL": "/openapi/v3/apis/argoproj.io/v1alpha1"}
  }
}`

const openAPIV3CoreV1 = `{
  "openapi": "3.0.0",
  "info": {"title": "mock-kubernetes", "version": "v1.34.0"},
  "paths": {},
  "components": {
    "schemas": {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {"type":"object","additionalProperties": true},
      "io.k8s.api.core.v1.Pod": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Pod"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.core.v1.Service": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Service"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.core.v1.ConfigMap": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"ConfigMap"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"data":{"type":"object","additionalProperties":{"type":"string"}}}
      },
      "io.k8s.api.core.v1.Secret": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Secret"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"data":{"type":"object","additionalProperties":{"type":"string"}},"type":{"type":"string"}}
      },
      "io.k8s.api.core.v1.Namespace": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Namespace"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.core.v1.Node": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"","version":"v1","kind":"Node"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      }
    }
  }
}`

const openAPIV3AppsV1 = `{
  "openapi": "3.0.0",
  "info": {"title": "mock-kubernetes", "version": "v1.34.0"},
  "paths": {},
  "components": {
    "schemas": {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {"type":"object","additionalProperties": true},
      "io.k8s.api.apps.v1.Deployment": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"apps","version":"v1","kind":"Deployment"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.apps.v1.ReplicaSet": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"apps","version":"v1","kind":"ReplicaSet"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.apps.v1.StatefulSet": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"apps","version":"v1","kind":"StatefulSet"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.apps.v1.DaemonSet": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"apps","version":"v1","kind":"DaemonSet"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      }
    }
  }
}`

const openAPIV3NetworkingV1 = `{
  "openapi": "3.0.0",
  "info": {"title": "mock-kubernetes", "version": "v1.34.0"},
  "paths": {},
  "components": {
    "schemas": {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {"type":"object","additionalProperties": true},
      "io.k8s.api.networking.v1.NetworkPolicy": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"networking.k8s.io","version":"v1","kind":"NetworkPolicy"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true}}
      }
    }
  }
}`

const openAPIV3GatewayV1 = `{
  "openapi": "3.0.0",
  "info": {"title": "mock-kubernetes", "version": "v1.34.0"},
  "paths": {},
  "components": {
    "schemas": {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {"type":"object","additionalProperties": true},
      "io.k8s.api.gateway.networking.v1.Gateway": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"gateway.networking.k8s.io","version":"v1","kind":"Gateway"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      },
      "io.k8s.api.gateway.networking.v1.HTTPRoute": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"gateway.networking.k8s.io","version":"v1","kind":"HTTPRoute"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      }
    }
  }
}`

const openAPIV3ArgoprojV1alpha1 = `{
  "openapi": "3.0.0",
  "info": {"title": "mock-kubernetes", "version": "v1.34.0"},
  "paths": {},
  "components": {
    "schemas": {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {"type":"object","additionalProperties": true},
      "io.argoproj.v1alpha1.Application": {
        "type":"object",
        "x-kubernetes-group-version-kind":[{"group":"argoproj.io","version":"v1alpha1","kind":"Application"}],
        "properties":{"apiVersion":{"type":"string"},"kind":{"type":"string"},"metadata":{"$ref":"#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta"},"spec":{"type":"object","additionalProperties": true},"status":{"type":"object","additionalProperties": true}}
      }
    }
  }
}`

var uidCounter int

func uid() string {
	uidCounter++
	return fmt.Sprintf("a%07d-b000-c000-d000-e00000%06d", uidCounter, uidCounter)
}
