package main

import (
	"bytes"
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
	fmt.Println("  NETWORK_IFACE      Private network interface (Auto-detected when unset)")
	fmt.Println("  ACME_EMAIL         Let's Encrypt registration email (Default: admin@lmbek.dk)")
	fmt.Println("  GHCR_USERNAME      GitHub username for GHCR image pull secret")
	fmt.Println("  GHCR_TOKEN         GitHub token for GHCR image pull secret")
}

func runCmd(name string, args ...string) error {
	fmt.Printf("▶ Running: %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
	return cmd.Run()
}

func runCmdOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func findPrivateNetworkInterface(privateIP string) (string, error) {
	if configured := os.Getenv("NETWORK_IFACE"); configured != "" {
		if err := exec.Command("ip", "link", "show", "dev", configured).Run(); err != nil {
			return "", fmt.Errorf("configured NETWORK_IFACE %q does not exist: %w", configured, err)
		}
		return configured, nil
	}

	fmt.Printf("═══ Waiting for private address %s ═══\n", privateIP)
	for attempt := 0; attempt < 120; attempt++ {
		output, err := exec.Command("ip", "-o", "-4", "addr", "show").Output()
		if err != nil {
			return "", fmt.Errorf("failed to inspect network interfaces: %w", err)
		}
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 4 && strings.SplitN(fields[3], "/", 2)[0] == privateIP {
				fmt.Printf("✔ Private network interface detected: %s\n", fields[1])
				return fields[1], nil
			}
		}
		time.Sleep(5 * time.Second)
	}

	return "", fmt.Errorf("private address %s was not attached within 10 minutes", privateIP)
}

func configureKernelAndFirewall(iface string) {
	fmt.Println("═══ Configuring Kernel Modules and Sysctl ═══")
	_ = os.WriteFile("/etc/modules-load.d/k3s.conf", []byte("overlay\nbr_netfilter\n"), 0644)
	_ = exec.Command("modprobe", "overlay").Run()
	_ = exec.Command("modprobe", "br_netfilter").Run()

	sysctlConfig := "net.bridge.bridge-nf-call-iptables = 1\nnet.bridge.bridge-nf-call-ip6tables = 1\nnet.ipv4.ip_forward = 1\nnet.ipv4.conf.all.rp_filter = 2\nnet.ipv4.conf.default.rp_filter = 2\n"
	_ = os.WriteFile("/etc/sysctl.d/99-kubernetes-k3s.conf", []byte(sysctlConfig), 0644)
	_ = exec.Command("sysctl", "--system").Run()

	fmt.Println("═══ Configuring UFW Firewall Rules ═══")
	if _, err := exec.LookPath("ufw"); err == nil {
		status, _ := runCmdOutput("ufw", "status")
		if strings.Contains(status, "active") {
			_ = exec.Command("ufw", "default", "allow", "routed").Run()
			_ = exec.Command("ufw", "allow", "22/tcp").Run()
			_ = exec.Command("ufw", "allow", "80/tcp").Run()
			_ = exec.Command("ufw", "allow", "443/tcp").Run()
			_ = exec.Command("ufw", "allow", "in", "on", iface).Run()
			_ = exec.Command("ufw", "route", "allow", "in", "on", iface).Run()
			_ = exec.Command("ufw", "allow", "from", "10.42.0.0/16").Run()
			_ = exec.Command("ufw", "allow", "from", "10.43.0.0/16").Run()
		}
	}
}

func setupMaster() error {
	masterIP := os.Getenv("MASTER_IP")
	if masterIP == "" {
		masterIP = "10.0.1.10"
	}
	token := os.Getenv("K3S_TOKEN")
	iface, err := findPrivateNetworkInterface(masterIP)
	if err != nil {
		return err
	}
	configureKernelAndFirewall(iface)

	fmt.Println("═══ Installing K3s Master Server ═══")
	execArgs := fmt.Sprintf("server --node-ip %s --advertise-address %s --flannel-iface %s --write-kubeconfig-mode 600 --secrets-encryption --tls-san %s --tls-san 127.0.0.1",
		masterIP, masterIP, iface, masterIP)

	if token != "" {
		execArgs += fmt.Sprintf(" --token %s", token)
	}

	installCmd := exec.Command("sh", "-c", "curl -sfL https://get.k3s.io | sh -")
	installCmd.Env = append(os.Environ(), "INSTALL_K3S_EXEC="+execArgs)
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("failed to install k3s master: %w", err)
	}

	fmt.Println("═══ Waiting for Master Node Readiness ═══")
	ready := false
	for i := 0; i < 30; i++ {
		out, err := runCmdOutput("kubectl", "get", "nodes")
		if err == nil && strings.Contains(out, "Ready") {
			fmt.Println("✔ Master node is Ready!")
			ready = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ready {
		return fmt.Errorf("master node did not become ready within 60 seconds")
	}

	// Configure global and user environment for kubeconfig
	_ = os.WriteFile("/etc/profile.d/k3s.sh", []byte("export KUBECONFIG=/etc/rancher/k3s/k3s.yaml\n"), 0755)
	if envData, err := os.ReadFile("/etc/environment"); err == nil {
		if !strings.Contains(string(envData), "KUBECONFIG") {
			_ = os.WriteFile("/etc/environment", append(envData, []byte("\nKUBECONFIG=/etc/rancher/k3s/k3s.yaml\n")...), 0644)
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		userKube := filepath.Join(home, ".kube")
		_ = os.MkdirAll(userKube, 0755)
		if data, err := os.ReadFile("/etc/rancher/k3s/k3s.yaml"); err == nil {
			_ = os.WriteFile(filepath.Join(userKube, "config"), data, 0600)
		}
		bashrcPath := filepath.Join(home, ".bashrc")
		if bashrcData, err := os.ReadFile(bashrcPath); err == nil {
			if !strings.Contains(string(bashrcData), "KUBECONFIG") {
				_ = os.WriteFile(bashrcPath, append(bashrcData, []byte("\nexport KUBECONFIG=/etc/rancher/k3s/k3s.yaml\n")...), 0644)
			}
		}
	}

	fmt.Println("✔ K3s Master setup completed successfully!")
	return nil
}

func setupWorker() error {
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
	iface, err := findPrivateNetworkInterface(workerIP)
	if err != nil {
		return err
	}
	configureKernelAndFirewall(iface)

	fmt.Printf("═══ Waiting for Master API endpoint at https://%s:6443/ping ═══\n", masterIP)
	ready := false
	for i := 0; i < 60; i++ {
		cmd := exec.Command("curl", "-k", "-s", "-f", fmt.Sprintf("https://%s:6443/ping", masterIP))
		if err := cmd.Run(); err == nil {
			fmt.Println("✔ Master API is online!")
			ready = true
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !ready {
		return fmt.Errorf("master API did not become ready within 3 minutes")
	}

	fmt.Println("═══ Installing K3s Worker Agent ═══")
	installCmd := exec.Command("sh", "-c", "curl -sfL https://get.k3s.io | sh -")
	installCmd.Env = append(os.Environ(),
		"K3S_URL="+fmt.Sprintf("https://%s:6443", masterIP),
		"K3S_TOKEN="+token,
		"INSTALL_K3S_EXEC="+fmt.Sprintf("agent --node-ip %s --flannel-iface %s", workerIP, iface),
	)
	installCmd.Stdout = os.Stdout
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("failed to install k3s worker agent: %w", err)
	}

	fmt.Println("✔ K3s Worker joined cluster successfully!")
	return nil
}

func bootstrapArgoCD() error {
	fmt.Println("═══ Step 1: Creating Namespaces (argocd, staging, production) ═══")
	namespaces := []string{"argocd", "staging", "production"}
	for _, ns := range namespaces {
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Env = append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", ns))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", ns, err)
		}
	}

	fmt.Println("═══ Step 2: Installing ArgoCD Manifests ═══")
	if err := runCmd("kubectl", "apply", "-n", "argocd", "-f", "https://raw.githubusercontent.com/argoproj/argo-cd/v2.14.11/manifests/install.yaml"); err != nil {
		return fmt.Errorf("failed to apply ArgoCD manifests: %w", err)
	}
	if err := runCmd("kubectl", "wait", "--for=condition=Available", "deployment/argocd-server", "-n", "argocd", "--timeout=300s"); err != nil {
		return fmt.Errorf("ArgoCD server did not become ready: %w", err)
	}

	ghcrUser := os.Getenv("GHCR_USERNAME")
	ghcrToken := os.Getenv("GHCR_TOKEN")
	if ghcrUser != "" && ghcrToken != "" {
		fmt.Println("═══ Step 3: Creating GHCR Image Pull Secret ═══")
		for _, ns := range []string{"staging", "production", "default"} {
			createCmd := exec.Command("kubectl", "create", "secret", "docker-registry", "ghcr-pull-secret",
				"--docker-server=ghcr.io",
				fmt.Sprintf("--docker-username=%s", ghcrUser),
				fmt.Sprintf("--docker-password=%s", ghcrToken),
				fmt.Sprintf("--docker-email=%s", "ci@lmbek.dk"),
				fmt.Sprintf("--namespace=%s", ns),
				"--dry-run=client", "-o", "yaml")
			createCmd.Env = append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
			secretYAML, err := createCmd.Output()
			if err != nil {
				return fmt.Errorf("failed to render GHCR pull secret for namespace %s: %w", ns, err)
			}
			applyCmd := exec.Command("kubectl", "apply", "-f", "-")
			applyCmd.Env = append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
			applyCmd.Stdin = bytes.NewReader(secretYAML)
			applyCmd.Stdout = os.Stdout
			applyCmd.Stderr = os.Stderr
			if err := applyCmd.Run(); err != nil {
				return fmt.Errorf("failed to apply GHCR pull secret to namespace %s: %w", ns, err)
			}
			if err := runCmd("kubectl", "patch", "serviceaccount", "default", "--namespace="+ns, "--type=merge", "--patch", `{"imagePullSecrets":[{"name":"ghcr-pull-secret"}]}`); err != nil {
				return fmt.Errorf("failed to attach GHCR pull secret in namespace %s: %w", ns, err)
			}
		}
	}

	fmt.Println("═══ Step 4: Applying Platform ArgoCD Applications ═══")
	platformArgocdDir := "../platform/argocd"
	for _, app := range []string{"staging.yml", "production.yml"} {
		appPath := filepath.Join(platformArgocdDir, app)
		if _, err := os.Stat(appPath); err != nil {
			return fmt.Errorf("platform application manifest %s is unavailable: %w", appPath, err)
		}
		if err := runCmd("kubectl", "apply", "-f", appPath); err != nil {
			return fmt.Errorf("failed to apply platform application %s: %w", app, err)
		}
	}

	fmt.Println("✔ ArgoCD bootstrap finished!")
	fmt.Println("Retrieve the initial admin password only when needed with:")
	fmt.Println("kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d")

	return nil
}

func setupCertManager() error {
	fmt.Println("═══ Installing cert-manager (v1.17.1) ═══")
	certManagerURL := "https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml"
	if err := runCmd("kubectl", "apply", "-f", certManagerURL); err != nil {
		return fmt.Errorf("failed to apply cert-manager CRDs and controller: %w", err)
	}

	fmt.Println("═══ Waiting for cert-manager webhook readiness ═══")
	if err := runCmd("kubectl", "wait", "--for=condition=Available", "deployment/cert-manager-webhook", "-n", "cert-manager", "--timeout=120s"); err != nil {
		return fmt.Errorf("cert-manager webhook did not become ready: %w", err)
	}

	fmt.Println("═══ Configuring Let's Encrypt ClusterIssuers (staging & production) ═══")
	acmeEmail := os.Getenv("ACME_EMAIL")
	if acmeEmail == "" {
		acmeEmail = "admin@lmbek.dk"
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
            ingressClassName: traefik
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
            ingressClassName: traefik
`, acmeEmail, acmeEmail)

	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Env = append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
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
