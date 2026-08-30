# 2-Node K3s Server Platform & Bootstrap CLI

This directory provides declarative cloud-init templates and a native Go 1.26 platform CLI to bootstrap and operate a 2-node K3s Kubernetes cluster (Master + Worker) on Hetzner Cloud.

---

## 🏗️ 2-Node Architecture

- **Server 1 (`k3s-master` / `10.0.1.10`)**: Control Plane, API Server, Traefik Ingress Controller, and ArgoCD GitOps Engine.
- **Server 2 (`k3s-worker` / `10.0.1.11`)**: Worker node dedicated to running application pods and websites.

Nodes communicate securely over the isolated Hetzner Cloud private network (`10.0.1.0/24`).

---

## ⚡ Automated Bootstrap via Cloud-Init

When provisioned via Terraform (`infrastructure/iac`), cloud-init runs automatically:
- `cloud-init-master.yaml` initializes the K3s control plane and binds to `10.0.1.10`.
- `cloud-init-worker.yaml` waits for the master API to become ready and connects as a worker agent.
- The master installs pinned cert-manager and Argo CD manifests, configures the issuers, and registers both platform applications.

Set `lmbek.dk`, `*.lmbek.dk`, and `*.staging.lmbek.dk` DNS `A` records to Terraform's `ingress_target_ip`. DNS must resolve before Let's Encrypt HTTP-01 certificates can become ready.

---

## 🛠️ Manual Operations via Go Platform CLI

You can also run commands directly on the servers using the Go 1.26 platform CLI:

```bash
# Display help and options
go run . help

# Set up Master node manually
go run . setup-master

# Join Worker node to Master
K3S_TOKEN="<cluster-token>" MASTER_IP="10.0.1.10" go run . setup-worker

# Install ArgoCD GitOps operator & apply platform manifests
GHCR_USERNAME="<user>" GHCR_TOKEN="<token>" go run . bootstrap-argocd

# Check cluster nodes, pods, and ingress routing
go run . status
```

---

## 🔄 Health Verification

Run on the Master node:
```bash
# Check node readiness (both nodes should be 'Ready')
kubectl get nodes -o wide

# Check core system pods
kubectl get pods -A

# Check ArgoCD pods
kubectl get pods -n argocd
```
