# Image URL to use all building/pushing image targets
IMG ?= remedy-controller:latest

all: test controller

# Run tests
test: fmt vet
	go test ./pkg/... -coverprofile cover.out

# Build manager binary
controller: fmt vet
	go build -o bin/remedy-controller remedy-controller/cmd/

# Run against the configured Kubernetes cluster in ~/.kube/config
run: fmt vet
	go run ./cmd/remedy_controller.go

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy:
	kubectl create -f deployment/deployment.yaml

# Run go fmt against code
fmt:
	go fmt ./pkg/... ./cmd/...

# Run go vet against code
vet:
	go vet ./pkg/... ./cmd/...

# Build the docker image
docker-build: test
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}