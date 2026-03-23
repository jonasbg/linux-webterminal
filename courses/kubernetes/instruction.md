# Kubernetes Basics

Learn to navigate a Kubernetes cluster using `kubectl`. This environment runs a mock Kubernetes API server with realistic cluster data. Every command you run uses the **real kubectl binary** talking to an API - just like production.

## What is Kubernetes?

Kubernetes is an API server with a database (etcd) behind it. That's it.

- You describe **what you want** (desired state) as YAML
- You send it to the API server (`kubectl apply`)
- Controllers watch the API and make reality match your desired state

Everything in Kubernetes is a **resource** stored in the API: Pods, Deployments, Services, ConfigMaps, Secrets.

---

## Part 1: Exploring the Cluster

### 1.1 Check the cluster

```
kubectl version
kubectl cluster-info
```

### 1.2 List namespaces

Namespaces organize resources into groups:

```
kubectl get namespaces
```

You should see `default`, `kube-system`, and `production`. Think of them as folders.

### 1.3 What's running?

```
kubectl get pods
```

This shows pods in the `default` namespace. To see other namespaces:

```
kubectl get pods -n kube-system
kubectl get pods -n production
kubectl get pods --all-namespaces
```

### 1.4 See everything

```
kubectl get all -n default
kubectl get all -n production
```

---

## Part 2: Understanding Resources

### 2.1 Pods - The smallest unit

A Pod is one or more containers running together:

```
kubectl get pods -n default
kubectl get pods -n default -o wide
```

The `-o wide` flag shows extra columns like IP addresses and nodes.

Get details about a specific pod:

```
kubectl describe pod nginx-7d4f8b7b94-abc12
```

Look at the raw YAML:

```
kubectl get pod nginx-7d4f8b7b94-abc12 -o yaml
```

**Key insight:** This YAML is exactly what's stored in the API database. `kubectl get` is just reading from the database.

### 2.2 Deployments - Managing Pods

Pods don't manage themselves. Deployments do:

```
kubectl get deployments
kubectl get deployments -n production
```

A Deployment says "I want 2 replicas of nginx". Kubernetes creates ReplicaSets and Pods to make it happen.

```
kubectl describe deployment nginx
```

Notice the `Replicas` field and the pod template.

### 2.3 Services - Network access

Services give pods a stable IP address:

```
kubectl get services
kubectl get services -n production
```

```
kubectl describe service nginx
```

The `Selector` field (`app: nginx`) matches pod labels. Any pod with that label gets traffic.

### 2.4 ConfigMaps - Configuration

ConfigMaps store non-sensitive configuration:

```
kubectl get configmaps
kubectl get configmaps -n production
```

```
kubectl describe configmap app-config
```

See the actual data:

```
kubectl get configmap app-config -o yaml
```

### 2.5 Secrets - Sensitive data

Secrets store sensitive data (base64 encoded, not encrypted):

```
kubectl get secrets -n production
```

```
kubectl get secret db-credentials -n production -o yaml
```

**Challenge:** The `data` values are base64 encoded. Decode them:

```
echo "YWRtaW4=" | base64 -d
echo "czNjcjN0LXBhc3N3MHJk" | base64 -d
```

**Key takeaway:** Kubernetes Secrets are NOT encrypted by default. Anyone with API access can read them.

### 2.6 StatefulSets - Stateful workloads

StatefulSets are like Deployments but for databases and other stateful apps:

```
kubectl get statefulsets -n production
kubectl describe statefulset postgres -n production
```

Notice the `volumeClaimTemplates` - each pod gets its own persistent storage.

---

## Part 3: Reading Logs

```
kubectl logs nginx-7d4f8b7b94-abc12
kubectl logs postgres-0 -n production
```

In a real cluster, logs come from the container's stdout/stderr. Here they're pre-populated samples.

---

## Part 4: Modifying Resources

### 4.1 Edit a deployment

```
kubectl edit deployment nginx
```

This opens the deployment YAML in vim. Try changing `replicas: 2` to `replicas: 3`, then save (`:wq`).

```
kubectl get deployment nginx
```

In a real cluster, Kubernetes would create a new pod. Here the API stores your change.

### 4.2 Edit a configmap

```
kubectl edit configmap app-config
```

Change `LOG_LEVEL` from `info` to `debug`. Save.

```
kubectl get configmap app-config -o yaml
```

Your change is persisted in the API.

### 4.3 Delete a pod

```
kubectl delete pod nginx-7d4f8b7b94-abc12
```

```
kubectl get pods
```

The pod is gone. In a real cluster, the Deployment controller would notice and create a replacement.

### 4.4 Create a resource from YAML

Create a new configmap by writing YAML:

```
cat > /tmp/my-config.yaml << 'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: default
data:
  greeting: "hello world"
  version: "1.0"
EOF
```

Apply it:

```
kubectl apply -f /tmp/my-config.yaml
```

Verify:

```
kubectl get configmap my-config -o yaml
```

---

## Part 5: The API Behind kubectl

Every kubectl command is just an HTTP request:

| kubectl command | HTTP request |
|----------------|-------------|
| `kubectl get pods` | `GET /api/v1/namespaces/default/pods` |
| `kubectl get pod nginx-xxx` | `GET /api/v1/namespaces/default/pods/nginx-xxx` |
| `kubectl delete pod nginx-xxx` | `DELETE /api/v1/namespaces/default/pods/nginx-xxx` |
| `kubectl apply -f file.yaml` | `POST` or `PUT` to the resource path |
| `kubectl get deployments` | `GET /apis/apps/v1/namespaces/default/deployments` |

kubectl is just a fancy HTTP client. The API server is just a REST API with a database.

### 5.1 Look at your kubeconfig

Your kubeconfig file tells kubectl where the API server is and how to authenticate:

```
cat ~/.kube/config
```

Notice the key fields:
- **server**: `https://127.0.0.1:6443` — the API server address (HTTPS, port 6443)
- **certificate-authority-data**: the CA certificate that kubectl uses to verify the server
- **client-certificate-data**: your client certificate (proves who you are)
- **client-key-data**: your private key (signs your requests)

This is mutual TLS (mTLS) — both sides verify each other. The server proves it's the real API server, and you prove you're an authorized user.

You can decode and inspect these certificates:

```
kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' | base64 -d
```

The CA cert files are also available at `/tmp/pki/`:

```
ls /tmp/pki/
```

### 5.2 See the actual HTTP requests with -v=8

kubectl has verbosity levels from 1-10. Level 8 shows the actual HTTP requests and responses — the curl commands that kubectl sends:

```
kubectl get pods -v=8
```

This outputs lines like:

```
I0323 12:00:00.000000  GET https://127.0.0.1:6443/api/v1/namespaces/default/pods 200 OK in 5 milliseconds
```

Try it with different commands to see the API calls:

```
kubectl get nodes -v=8
kubectl get services -n production -v=8
kubectl get pod nginx-7d4f8b7b94-abc12 -o yaml -v=8
```

At verbosity level 8 you'll see:
- The full URL (which API path kubectl calls)
- Request and response headers
- The HTTP method (GET, POST, PUT, DELETE)
- Response status codes (200, 404, etc.)

**Key insight:** There is nothing magic about kubectl. You could do the same thing with curl:

```
curl --cacert /tmp/pki/ca.crt --cert /tmp/pki/client.crt --key /tmp/pki/client.key https://127.0.0.1:6443/api/v1/namespaces/default/pods
```

That curl command does exactly what `kubectl get pods -o json` does — it sends a GET request to the API server with TLS client authentication.

Try other API endpoints directly:

```
curl -s --cacert /tmp/pki/ca.crt --cert /tmp/pki/client.crt --key /tmp/pki/client.key https://127.0.0.1:6443/api/v1/nodes | head -20
```

---

## Challenges

1. List all pods across all namespaces. Which namespace has the most?
2. Decode the production database secret. What are the credentials?
3. Edit the `api-server` deployment in production to use image `mycompany/api-server:2.2.0`
4. Create a new Secret in the default namespace with your own base64-encoded data
5. Delete a pod in production and then check `kubectl get pods -n production` - what changed?
6. Look at the production `api-config` ConfigMap. What database is the API server connecting to?

## Key Takeaways

- Kubernetes is an API server with a database - everything is a resource
- `kubectl` is just an HTTP client that formats output nicely
- Resources have: `apiVersion`, `kind`, `metadata`, and `spec`
- Namespaces organize resources (like folders)
- Deployments manage Pods, Services route traffic, ConfigMaps/Secrets store configuration
- Secrets are base64 encoded, not encrypted - treat API access as the security boundary
