# Akash Provider Daemon

[![tests](https://github.com/akash-network/provider/actions/workflows/tests.yaml/badge.svg)](https://github.com/akash-network/provider/actions/workflows/tests.yaml)

This folder contains the Akash Provider Daemon. This piece of software listens to events emitted from the Akash blockchain (code in `../app/app.go`) and takes actions on a connected Kubernetes cluster to provision compute capacity based on the bids that the configured provider key wins. The following are the pieces of the daemon:

## Development environment

[This doc](https://github.com/akash-network/node/blob/master/_docs/development-environment.md) guides through setting up local development environment 

## Structure

### [`bidengine`](./bidengine)

The bid engine queries for any existing orders on chain, and based on the on-chain provider configuration, places bids on behalf of the configured provider based on configured selling prices for resources. The daemon listens for changes in the configuration so users can use automation tooling to dynamically change the prices they are charging w/o restarting the daemon. You can see the key management code for `provider` tx signing in `cmd/run.go`.

### [`cluster`](./cluster)

The cluster package contains the necessary code for interacting with clusters of compute that a `provider` is offering on the open marketplace to deploy orders on behalf of users creating `deployments` based on `manifest`s. Right now only `kubernetes` is supported as a backend, but `providers` could easily implement other cluster management solutions such as OpenStack, VMWare, OpenShift, etc...

### [`cmd`](./cmd)

The `cobra` command line utility that wraps the rest of the code here and is buildable.

### [`event`](./event)

Declares the pubsub events that the `provider` needs to take action on won leases and received manifests.

### [`gateway`](./gateway)

Contains hanlder code for the rest server exposed by the `provider`

### [`manifest`](./manifest)

## TPM - Running Tests with Dooor TEE-GO-API

### Prerequisites Installation

#### 1. Install Essential Libraries and Go

```bash
# Update system packages
sudo apt update && sudo apt upgrade -y

# Install essential tools
sudo apt install -y build-essential curl wget jq make unzip npm coreutils

# Install Go (>= 1.21.0)
sudo rm -rf /usr/local/go || true
wget https://go.dev/dl/go1.21.8.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.21.8.linux-amd64.tar.gz
rm go1.21.8.linux-amd64.tar.gz
if ! grep -q "/usr/local/go/bin" ~/.bashrc; then
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
fi
export PATH=$PATH:/usr/local/go/bin
```

#### 2. Install Direnv and Docker

```bash
# Install Direnv (>= 2.32.x)
sudo apt remove -y direnv || true
wget https://github.com/direnv/direnv/releases/download/v2.32.3/direnv.linux-amd64 -O direnv
chmod +x direnv
sudo mv direnv /usr/local/bin/direnv
if ! grep -q 'eval "$(direnv hook bash)"' ~/.bashrc; then
    echo 'eval "$(direnv hook bash)"' >> ~/.bashrc
fi
source ~/.bashrc

# Install Docker
sudo apt install -y docker.io
sudo systemctl enable --now docker
sudo usermod -aG docker $USER
```

Note: After this step, restart your terminal for changes to take effect.

#### 3. Install Kubernetes Tools and Setup Environment

```bash
# Install QEMU and related tools
sudo apt-get install -y qemu qemu-user-static binfmt-support

# Configure Docker buildx
docker run --privileged --rm tonistiigi/binfmt --install all
docker buildx create --name mybuilder --use

# Create and navigate to repository directory
mkdir -p ~/go/src/github.com/akash-network
cd ~/go/src/github.com/akash-network

# Clone repositories
git clone https://github.com/akash-network/node.git
git clone https://github.com/Dooor-AI/akash-provider.git

cd ~/go/src/github.com/Dooor-AI/akash-provider
direnv allow

# Install Kind
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind

# Install kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
chmod +x kubectl
sudo mv kubectl /usr/local/bin/

# Install additional tools
sudo snap install --edge grpcurl
sudo snap install --edge tqdm

# Set environment variables
export AP_RUN_NAME=provider-run-001
direnv allow

cd ~/go/src/github.com/akash-network/akash-provider/_run/kube
```

### Running the Test Environment

#### 1. Setup Kubernetes Cluster
```bash
make kube-cluster-setup
```

If timeout occurs, run:
```bash
make kube-cluster-delete
make clean
make init
KUBE_ROLLOUT_TIMEOUT=500 make kube-cluster-setup
```

#### 2. Start Akash Node (Terminal 2)
```bash
cd ~/go/src/github.com/akash-network/provider/_run/kube
make node-run
```

#### 3. Create Provider (Terminal 1)
```bash
cd ~/go/src/github.com/akash-network/provider/_run/kube
make provider-create
```

#### 4. Run Provider (Terminal 3)
```bash
cd ~/go/src/github.com/akash-network/provider/_run/kube
make provider-run
```

### Useful Commands

```bash
# Create deployment
make deployment-create
# If you want to create a deployment with a specific DSEQ
make deployment-create DSEQ=99

# Query status
make query-deployments
make query-orders
make query-bids

# Lease operations
make lease-create
make query-leases
make provider-status

# Manifest operations
make send-manifest
make provider-lease-status
make provider-lease-ping

# View logs
make provider-lease-logs
```

### Port Forwarding
```bash
kubectl port-forward web-5495d757bd-sng9q 5000:5000 -n 7dqtsniu0rrmtjup63248uh21sbvrg2bbmau6132hvdak
```

### Container Shell Access
```bash
kubectl exec -it web-79c6cd9456-x4bvc -n 79vr987pqoofi729clag6qdt7ub36ijrsg1nk0t9mv9og -c sidecar-tee -- sh
```

### Working with Protected Folders

Create protected folder:
```bash
mkdir -p /home/bruno/protected_folder
ls -la /home/bruno
```

Interact with the API:
```bash
curl -X POST http://localhost:5000/seal \
    -H "Content-Type: application/json" \
    -d '{"directory":"/home/bruno/protected_folder","secret":"My secret info"}'
```

