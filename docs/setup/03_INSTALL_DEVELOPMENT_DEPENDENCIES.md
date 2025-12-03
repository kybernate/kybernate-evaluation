# Install Development Dependencies

Um an Kybernate zu entwickeln (Shim, Controller, Agent), werden folgende Tools benötigt.
Die Standard-Pakete von Ubuntu 24.04 (Noble) sind größtenteils aktuell, für Go benötigen wir jedoch eine neuere Version für Kubebuilder.

## 1. Go (Golang)

Wir nutzen das Backports-PPA, um die aktuellste Go-Version zu erhalten (z.B. 1.24+).

```bash
sudo add-apt-repository ppa:longsleep/golang-backports
sudo apt update
sudo apt install -y golang-go

# Prüfen der Version
go version
which go  # -> /usr/bin/go
```

`/usr/bin` ist (für alle Benutzer inklusive `root`) bereits im `PATH`. Zusätzlich sollten wir das `GOPATH/bin` Verzeichnis hinzufügen, damit Tools, die via `go install` installiert werden, gefunden werden.

```bash
# Für den eigenen Benutzer
echo 'export PATH=$PATH:$(go env GOPATH)/bin' >> ~/.bashrc
source ~/.bashrc

# Für root (z.B. falls sudo -i genutzt wird)
echo 'export PATH=$PATH:$(go env GOPATH)/bin' | sudo tee -a /root/.bashrc
```

## 2. Protocol Buffers (gRPC)

Für die Kommunikation zwischen Node-Agent und Shim wird gRPC verwendet. Wir benötigen den Compiler `protoc` und die Go-Plugins.

### Protoc Compiler

```bash
sudo apt install -y protobuf-compiler
protoc --version
```

### Go Plugins für Protoc

Diese müssen via `go install` installiert werden. Wir verwenden `@latest`, um Kompatibilität mit der aktuellen Go-Version sicherzustellen.

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

## 3. Build Tools

```bash
sudo apt install -y make git build-essential
```

## 4. Kubernetes Development Tools

### Kubebuilder (Optional, für Controller)

Falls wir das Controller-Gerüst mit Kubebuilder generieren wollen:

```bash
curl -L -o kubebuilder https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)
chmod +x kubebuilder && sudo mv kubebuilder /usr/local/bin/
```

### Kustomize

```bash
curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash
sudo mv kustomize /usr/local/bin/
```
