# K3s Bootstrap

`cloud-init.yaml` is the complete immutable bootstrap for the single K3s server.

---

## Automated setup

Terraform passes this file to the server at creation. It installs pinned K3s,
cert-manager, and Argo CD versions, then registers the production and staging GitOps
applications. The bootstrap skips a full OS package refresh when the standard Ubuntu
image already contains `curl`, installs cert-manager and Argo CD concurrently, and
waits only for the CRDs needed to register those applications. There are no manual
server setup steps.

Set the `lmbek.dk` DNS `A` record to Terraform's `ingress_target_ip`. DNS must resolve
before the Let's Encrypt HTTP-01 certificate can become ready.

---

Operational changes belong in Terraform, this cloud-init file, or the platform GitOps
repository. Do not run setup commands manually on production.
