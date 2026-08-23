package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	var err error

	switch command {
	case "setup-master", "master":
		err = setupMaster()
	case "setup-worker", "worker":
		err = setupWorker()
	case "setup-certmanager", "cert-manager", "certs":
		err = setupCertManager()
	case "bootstrap-argocd", "argocd":
		err = bootstrapArgoCD()
	case "status":
		err = showStatus()
	case "all-master":
		if err = setupMaster(); err == nil {
			if err = setupCertManager(); err == nil {
				err = bootstrapArgoCD()
			}
		}
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[ERROR] %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Server Platform CLI — 2-Node K3s Cluster & GitOps Bootstrapper")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  go run . <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  setup-master       Install K3s control plane server on Master node")
	fmt.Println("  setup-worker       Join Worker node to Master K3s cluster over private network")
	fmt.Println("  setup-certmanager  Install cert-manager and Let's Encrypt ClusterIssuers (staging/prod)")
	fmt.Println("  bootstrap-argocd   Install ArgoCD, namespaces, and initial GitOps applications")
	fmt.Println("  all-master         Run setup-master, setup-certmanager, and bootstrap-argocd")
	fmt.Println("  status             Display cluster node status, certificates, pods, and ingress info")
	fmt.Println("  help               Show this help message")
	fmt.Println()
	fmt.Println("Environment Variables:")
	fmt.Println("  K3S_TOKEN          Cluster join token (Required for worker / Recommended for master)")
	fmt.Println("  MASTER_IP          Master private IP (Default: 10.0.1.10)")
	fmt.Println("  WORKER_IP          Worker private IP (Default: 10.0.1.11)")
	fmt.Println("  NETWORK_IFACE      Private network interface (Default: eth1)")
	fmt.Println("  ACME_EMAIL         Let's Encrypt registration email (Default: admin@lmbek.local)")
	fmt.Println("  GHCR_USERNAME      GitHub username for GHCR image pull secret")
	fmt.Println("  GHCR_TOKEN         GitHub token for GHCR image pull secret")
}

func runCmd(name string, args ...string) error {
	fmt.Printf("▶ Running: %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func configureKernelAndFirewall(isMaster bool) {
	fmt.Println("═══ Configuring Kernel Modules and Sysctl ═══")
	_ = os.WriteFile("/etc/modules-load.d/k3s.conf", []byte("overlay\nbr_netfilter\n"), 0644)
	_ = exec.Command("modprobe", "overlay").Run()
	_ = exec.Command("modprobe", "br_netfilter").Run()

	sysctlConfig := "net.bridge.bridge-nf-call-iptables = 1\nnet.bridge.bridge-nf-call-ip6tables = 1\nnet.ipv4.ip_forward = 1\n"
	_ = os.WriteFile("/etc/sysctl.d/99-kubernetes-k3s.conf", []byte(sysctlConfig), 0644)
	_ = exec.Command("sysctl", "--system").Run()

	fmt.Println("═══ Configuring UFW Firewall Rules ═══")
	if _, err := exec.LookPath("ufw"); err == nil {
		status, _ := runCmdOutput("ufw", "status")
		if strings.Contains(status, "active") {
			_ = exec.Command("ufw", "allow", "22/tcp").Run()
			_ = exec.Command("ufw", "allow", "in", "on", "eth1").Run()
			if isMaster {
				_ = exec.Command("ufw", "allow", "80/tcp").Run()
				_ = exec.Command("ufw", "allow", "443/tcp").Run()
			}
		}
	}
}

func setupMaster() error {
	configureKernelAndFirewall(true)

	masterIP := os.Getenv("MASTER_IP")
	if masterIP == "" {
		masterIP = "10.0.1.10"
	}
	token := os.Getenv("K3S_TOKEN")
	iface := os.Getenv("NETWORK_IFACE")
	if iface == "" {
		iface = "eth1"
	}

	fmt.Println("═══ Installing K3s Master Server ═══")
	execArgs := fmt.Sprintf("server --node-ip %s --advertise-address %s --flannel-iface %s --write-kubeconfig-mode 644 --tls-san %s --tls-san 127.0.0.1",
		masterIP, masterIP, iface, masterIP)

	if token != "" {
		execArgs += fmt.Sprintf(" --token %s", token)
	}

	installCmd := fmt.Sprintf("curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='%s' sh -", execArgs)
	if err := exec.Command("sh", "-c", installCmd).Run(); err != nil {
		return fmt.Errorf("failed to install k3s master: %w", err)
	}

	fmt.Println("═══ Waiting for Master Node Readiness ═══")
	for i := 0; i < 30; i++ {
		out, err := runCmdOutput("kubectl", "get", "nodes")
		if err == nil && strings.Contains(out, "Ready") {
			fmt.Println("✔ Master node is Ready!")
			break
		}
		time.Sleep(2 * time.Second)
	}

	// Copy kubeconfig to user home if possible
	home, err := os.UserHomeDir()
	if err == nil {
		userKube := filepath.Join(home, ".kube")
		_ = os.MkdirAll(userKube, 0755)
		if data, err := os.ReadFile("/etc/rancher/k3s/k3s.yaml"); err == nil {
			_ = os.WriteFile(filepath.Join(userKube, "config"), data, 0600)
		}
	}

	fmt.Println("✔ K3s Master setup completed successfully!")
	return nil
}

func setupWorker() error {
	configureKernelAndFirewall(false)

	masterIP := os.Getenv("MASTER_IP")
	if masterIP == "" {
		masterIP = "10.0.1.10"
	}
	workerIP := os.Getenv("WORKER_IP")
	if workerIP == "" {
		workerIP = "10.0.1.11"
	}
	token := os.Getenv("K3S_TOKEN")
	if token == "" {
		return fmt.Errorf("K3S_TOKEN environment variable is required to join worker node")
	}
	iface := os.Getenv("NETWORK_IFACE")
	if iface == "" {
		iface = "eth1"
	}

	fmt.Printf("═══ Waiting for Master API endpoint at https://%s:6443/ping ═══\n", masterIP)
	for i := 0; i < 60; i++ {
		cmd := exec.Command("curl", "-k", "-s", "-f", fmt.Sprintf("https://%s:6443/ping", masterIP))
		if err := cmd.Run(); err == nil {
			fmt.Println("✔ Master API is online!")
			break
		}
		time.Sleep(3 * time.Second)
	}

	fmt.Println("═══ Installing K3s Worker Agent ═══")
	installCmd := fmt.Sprintf("curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s INSTALL_K3S_EXEC='agent --node-ip %s --flannel-iface %s' sh -",
		masterIP, token, workerIP, iface)

	if err := exec.Command("sh", "-c", installCmd).Run(); err != nil {
		return fmt.Errorf("failed to install k3s worker agent: %w", err)
	}

	fmt.Println("✔ K3s Worker joined cluster successfully!")
	return nil
}

func bootstrapArgoCD() error {
	fmt.Println("═══ Step 1: Creating Namespaces (argocd, staging, production) ═══")
	namespaces := []string{"argocd", "staging", "production"}
	for _, ns := range namespaces {
		_ = runCmd("kubectl", "create", "namespace", ns, "--dry-run=client", "-o", "yaml")
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", ns))
		_ = cmd.Run()
	}

	fmt.Println("═══ Step 2: Installing ArgoCD Manifests ═══")
	if err := runCmd("kubectl", "apply", "-n", "argocd", "-f", "https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml"); err != nil {
		return fmt.Errorf("failed to apply ArgoCD manifests: %w", err)
	}

	ghcrUser := os.Getenv("GHCR_USERNAME")
	ghcrToken := os.Getenv("GHCR_TOKEN")
	if ghcrUser != "" && ghcrToken != "" {
		fmt.Println("═══ Step 3: Creating GHCR Image Pull Secret ═══")
		for _, ns := range []string{"staging", "production", "default"} {
			_ = runCmd("kubectl", "create", "secret", "docker-registry", "ghcr-pull-secret",
				"--docker-server=ghcr.io",
				fmt.Sprintf("--docker-username=%s", ghcrUser),
				fmt.Sprintf("--docker-password=%s", ghcrToken),
				fmt.Sprintf("--docker-email=%s", "ci@lmbek.local"),
				fmt.Sprintf("--namespace=%s", ns),
				"--dry-run=client", "-o", "yaml")
		}
	}

	fmt.Println("═══ Step 4: Applying Platform ArgoCD Applications ═══")
	platformArgocdDir := "../platform/argocd"
	for _, app := range []string{"staging.yml", "production.yml"} {
		appPath := filepath.Join(platformArgocdDir, app)
		if _, err := os.Stat(appPath); err == nil {
			_ = runCmd("kubectl", "apply", "-f", appPath)
		}
	}

	fmt.Println("✔ ArgoCD bootstrap finished!")
	time.Sleep(3 * time.Second)

	adminPassOut, err := runCmdOutput("kubectl", "-n", "argocd", "get", "secret", "argocd-initial-admin-secret", "-o", "jsonpath={.data.password}")
	if err == nil && adminPassOut != "" {
		decoded, err := base64.StdEncoding.DecodeString(adminPassOut)
		if err == nil {
			fmt.Println("\n────────────────────────────────────────────────────────")
			fmt.Printf("ArgoCD Admin Credentials:\n  Username: admin\n  Password: %s\n", string(bytes.TrimSpace(decoded)))
			fmt.Println("────────────────────────────────────────────────────────")
		}
	}

	return nil
}

func setupCertManager() error {
	fmt.Println("═══ Installing cert-manager (v1.17.1) ═══")
	certManagerURL := "https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml"
	if err := runCmd("kubectl", "apply", "-f", certManagerURL); err != nil {
		return fmt.Errorf("failed to apply cert-manager CRDs and controller: %w", err)
	}

	fmt.Println("═══ Waiting for cert-manager webhook readiness ═══")
	_ = runCmd("kubectl", "wait", "--for=condition=Available", "deployment/cert-manager-webhook", "-n", "cert-manager", "--timeout=120s")

	fmt.Println("═══ Configuring Let's Encrypt ClusterIssuers (staging & production) ═══")
	acmeEmail := os.Getenv("ACME_EMAIL")
	if acmeEmail == "" {
		acmeEmail = "admin@lmbek.local"
	}

	clusterIssuerYAML := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-staging
spec:
  acme:
    server: https://acme-staging-v02.api.letsencrypt.org/directory
    email: %s
    privateKeySecretRef:
      name: letsencrypt-staging-account-key
    solvers:
      - http01:
          ingress:
            class: traefik
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-prod
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: %s
    privateKeySecretRef:
      name: letsencrypt-prod-account-key
    solvers:
      - http01:
          ingress:
            class: traefik
`, acmeEmail, acmeEmail)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(clusterIssuerYAML)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create ClusterIssuers: %w", err)
	}

	fmt.Println("✔ cert-manager and Let's Encrypt ClusterIssuers configured successfully!")
	return nil
}

func showStatus() error {
	fmt.Println("═══ K3s Cluster Nodes ═══")
	_ = runCmd("kubectl", "get", "nodes", "-o", "wide")

	fmt.Println("\n═══ cert-manager & ClusterIssuers ═══")
	_ = runCmd("kubectl", "get", "clusterissuers")
	_ = runCmd("kubectl", "get", "certificates", "--all-namespaces")

	fmt.Println("\n═══ Ingress Routes & TLS ═══")
	_ = runCmd("kubectl", "get", "ingress", "--all-namespaces")

	fmt.Println("\n═══ Workload Pods (production & staging) ═══")
	_ = runCmd("kubectl", "get", "pods", "-n", "production")
	_ = runCmd("kubectl", "get", "pods", "-n", "staging")

	return nil
}
