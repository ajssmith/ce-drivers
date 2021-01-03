$ go build -buildmode=plugin -o podman.so plug-ins/podman/podman.go

$ go build -buildmode=plugin -o docker.so plug-ins/docker/docker.go

$ go build -o skupper-host cmd/main.go

$ ./skupper-host docker

$ sudo podman system service -t 0

$ ./skupper-host podman

