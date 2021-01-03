package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetworktypes "github.com/docker/docker/api/types/network"
	dockerapi "github.com/docker/docker/client"
	dockermessage "github.com/docker/docker/pkg/jsonmessage"
	dockerstdcopy "github.com/docker/docker/pkg/stdcopy"

	"github.com/ajssmith/ce-drivers/driver"
	skupperutils "github.com/skupperproject/skupper/pkg/utils"
)

const (
	// defaultTimeout is the default timeout of short running docker operations.
	// Value is slightly offset from 2 minutes to make timeouts due to this
	// constant recognizable.
	defaultTimeout = 2*time.Minute - 1*time.Second

	// defaultShmSize is the default ShmSize to use (in bytes) if not specified.
	defaultShmSize = int64(1024 * 1024 * 64)

	// defaultImagePullingProgressReportInterval is the default interval of image pulling progress reporting.
	defaultImagePullingProgressReportInterval = 10 * time.Second
)

type dockerClient struct {
	client                   *dockerapi.Client
	timeout                  time.Duration
	imagePullProgessDeadline time.Duration
}

type ImageNotFoundError struct {
	ID string
}

var Driver dockerClient

func getTimeoutContext(d *dockerClient) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d.timeout)
}

func newContainerSpec(name string) *dockertypes.ContainerCreateConfig {
	//TODO, what should be setup here
	containerCfg := &dockercontainer.Config{}
	hostCfg := &dockercontainer.HostConfig{}
	networkCfg := &dockernetworktypes.NetworkingConfig{}

	opts := &dockertypes.ContainerCreateConfig{
		Name:             name,
		Config:           containerCfg,
		HostConfig:       hostCfg,
		NetworkingConfig: networkCfg,
	}
	return opts
}

func (c *dockerClient) New() error {
	fmt.Println("Inside docker plugin new")
	client, err := dockerapi.NewClientWithOpts(dockerapi.FromEnv, dockerapi.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("Couldn't connect to docker: %w", err)
	}

	Driver.client = client
	Driver.timeout = driver.DefaultTimeout
	Driver.imagePullProgessDeadline = driver.DefaultImagePullingProgressReportInterval

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()
	Driver.client.NegotiateAPIVersion(ctx)

	return nil
}

func getCancelableContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func contextError(ctx context.Context) error {
	if ctx.Err() == context.DeadlineExceeded {
		return operationTimeout{err: ctx.Err()}
	}
	return ctx.Err()
}

type operationTimeout struct {
	err error
}

func (e operationTimeout) Error() string {
	return fmt.Sprintf("operation timeout: %v", e.err)
}

func base64EncodeAuth(auth dockertypes.AuthConfig) (string, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(auth); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(buf.Bytes()), nil
}

// progress is a wrapper of dockermessage.JSONMessage with a lock protecting it.
type progress struct {
	sync.RWMutex
	// message stores the latest docker json message.
	message *dockermessage.JSONMessage
	// timestamp of the latest update.
	timestamp time.Time
}

func newProgress() *progress {
	return &progress{timestamp: time.Now()}
}

func (p *progress) set(msg *dockermessage.JSONMessage) {
	p.Lock()
	defer p.Unlock()
	p.message = msg
	p.timestamp = time.Now()
}

func (p *progress) get() (string, time.Time) {
	p.RLock()
	defer p.RUnlock()
	if p.message == nil {
		return "No progress", p.timestamp
	}
	// The following code is based on JSONMessage.Display
	var prefix string
	if p.message.ID != "" {
		prefix = fmt.Sprintf("%s: ", p.message.ID)
	}
	if p.message.Progress == nil {
		return fmt.Sprintf("%s%s", prefix, p.message.Status), p.timestamp
	}
	return fmt.Sprintf("%s%s %s", prefix, p.message.Status, p.message.Progress.String()), p.timestamp
}

type progressReporter struct {
	*progress
	image                     string
	cancel                    context.CancelFunc
	stopCh                    chan struct{}
	imagePullProgressDeadline time.Duration
}

func newProgressReporter(image string, cancel context.CancelFunc, imagePullProgressDeadline time.Duration) *progressReporter {
	return &progressReporter{
		progress:                  newProgress(),
		image:                     image,
		cancel:                    cancel,
		stopCh:                    make(chan struct{}),
		imagePullProgressDeadline: imagePullProgressDeadline,
	}
}

func (p *progressReporter) start() {
	go func() {
		ticker := time.NewTicker(defaultImagePullingProgressReportInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, timestamp := p.progress.get()
				// If there is no progress for p.imagePullProgressDeadline, cancel the operation.
				if time.Since(timestamp) > p.imagePullProgressDeadline {
					//log.Printf("Cancel pulling image %q because of no progress for %v, latest progress: %q", p.image, p.imagePullProgressDeadline, progress)
					//log.Println()
					p.cancel()
					return
				}
				//log.Printf("Pulling image %q: %q", p.image, progress)
				//log.Println()
			case <-p.stopCh:
				//progress, _ := p.progress.get()
				//log.Printf("Stop pulling image %q: %q", p.image, progress)
				//log.Println()
				return
			}
		}
	}()
}

func (p *progressReporter) stop() {
	close(p.stopCh)
}

func (c *dockerClient) ImagesPull(refStr string, options driver.ImagePullOptions) ([]string, error) {
	// TODO: return common []string
	fmt.Println("In docker pull images")
	// RegistryAuth is the base64 encoded credentials for the registry
	auth := dockertypes.AuthConfig{}
	base64Auth, err := base64EncodeAuth(auth)
	if err != nil {
		return nil, err
	}
	opts := dockertypes.ImagePullOptions{}
	opts.RegistryAuth = base64Auth

	ctx, cancel := getCancelableContext()
	defer cancel()
	resp, err := c.client.ImagePull(ctx, refStr, opts)
	if err != nil {
		return nil, err
	}
	defer resp.Close()
	reporter := newProgressReporter(refStr, cancel, 10*time.Second)
	reporter.start()
	defer reporter.stop()
	decoder := json.NewDecoder(resp)
	for {
		var msg dockermessage.JSONMessage
		err := decoder.Decode(&msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		reporter.set(&msg)
	}
	return nil, nil
}

func (c *dockerClient) ImageInspect(id string) (*driver.ImageInspect, error) {
	fmt.Println("In docker inspect image")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	data, _, err := c.client.ImageInspectWithRaw(ctx, id)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}

	image := &driver.ImageInspect{
		ID:       data.ID,
		Size:     data.Size,
		RepoTags: data.RepoTags,
	}
	return image, nil
}

func (c *dockerClient) ImagesList(options driver.ImageListOptions) ([]driver.ImageSummary, error) {
	fmt.Println("In docker list images")
	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()
	images, err := c.client.ImageList(ctx, dockertypes.ImageListOptions{})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	var summary []driver.ImageSummary
	for _, image := range images {
		summary = append(summary, driver.ImageSummary{
			ID:          image.ID,
			Labels:      image.Labels,
			RepoTags:    image.RepoTags,
			RepoDigests: image.RepoDigests,
		})
	}
	return summary, nil
}

func (c *dockerClient) ContainerCreate(image string) (driver.ContainerCreateResponse, error) {
	fmt.Println("Inside docker container create")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	opts := newContainerSpec("skupper-router")
	opts.Config.Image = image

	ccb, err := c.client.ContainerCreate(ctx, opts.Config, opts.HostConfig, opts.NetworkingConfig, nil, opts.Name)
	if err != nil {
		return driver.ContainerCreateResponse{}, err
	}
	return driver.ContainerCreateResponse{ID: ccb.ID}, nil
}

func (c *dockerClient) ContainerStart(id string) error {
	fmt.Println("Inside docker start container")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	err := c.client.ContainerStart(ctx, id, dockertypes.ContainerStartOptions{})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}

	return err
}

func (c *dockerClient) ContainerWait(id string, status string, timeout time.Duration, interval time.Duration) error {
	fmt.Println("Inside docker container wait")
	var container dockertypes.ContainerJSON
	var err error

	ctx, cancel := context.WithTimeout(context.TODO(), timeout)
	defer cancel()

	err = skupperutils.RetryWithContext(ctx, interval, func() (bool, error) {
		container, err = c.client.ContainerInspect(ctx, id)
		if err != nil {
			return false, nil
		}
		return container.State.Status == status, nil
	})
	return err
}

func (c *dockerClient) ContainerList(driver.ContainerListOptions) ([]driver.Container, error) {
	fmt.Println("Inside docker container list")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	// TODO: convert options
	containers, err := c.client.ContainerList(ctx, dockertypes.ContainerListOptions{})
	var dc []driver.Container
	if ctxErr := contextError(ctx); ctxErr != nil {
		return dc, ctxErr
	}
	if err != nil {
		return dc, err
	}
	for _, container := range containers {
		// TODO all fields
		dc = append(dc, driver.Container{
			ID:      container.ID,
			Names:   container.Names,
			Image:   container.Image,
			ImageID: container.ImageID,
			Command: container.Command,
			//Ports:   container.Ports,
			Labels: container.Labels,
			State:  container.State,
			Status: container.Status,
			//Mounts:  container.Mounts,
		})
	}
	return dc, nil
}

func (c *dockerClient) ContainerInspect(id string) (*driver.InspectContainerData, error) {
	fmt.Println("Inside docker container inspect")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	container, err := c.client.ContainerInspect(ctx, id)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}
	icd := &driver.InspectContainerData{
		ID: container.ID,
		//		Created: container.Created,
		Path: container.Path,
		Args: container.Args,
		// State: container.State,
		Image: container.Image,
		//ImageName: container.ImageName,
		Name: container.Name,
	}

	return icd, err
}

func (c *dockerClient) ContainerStop(id string) error {
	fmt.Println("Inside docker stop container")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	err := c.client.ContainerStop(ctx, id, nil)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (c *dockerClient) ContainerRemove(id string) error {
	fmt.Println("Inside docker container remove")
	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	err := c.client.ContainerRemove(ctx, id, dockertypes.ContainerRemoveOptions{})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (c *dockerClient) NetworkCreate(name string, options driver.NetworkCreateOptions) (driver.NetworkCreateResponse, error) {
	fmt.Println("Inside docker network create")

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	ncr, err := c.client.NetworkCreate(ctx, name, dockertypes.NetworkCreate{
		CheckDuplicate: true,
		Driver:         "bridge",
		Options: map[string]string{
			"com.docker.network.bridge.name":                 "skupper0",
			"com.docker.network.bridge.enable_icc":           "true",
			"com.docker.network.bridge.enable_ip_masquerade": "true",
		},
	})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return driver.NetworkCreateResponse{}, ctxErr
	}
	return driver.NetworkCreateResponse{ID: ncr.ID, Warning: ncr.Warning}, err
}

func (c *dockerClient) NetworkInspect(id string) (driver.NetworkResource, error) {
	fmt.Println("Inside docker network inspect")
	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	nr, err := c.client.NetworkInspect(ctx, id, dockertypes.NetworkInspectOptions{})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return driver.NetworkResource{}, ctxErr
	}
	return driver.NetworkResource{Name: nr.Name}, err
}

func (c *dockerClient) NetworkRemove(id string) error {
	//	force := true
	fmt.Println("Inside docker network remove for: ", id)
	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	err := c.client.NetworkRemove(ctx, id)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (c *dockerClient) NetworkConnect(id string, container string, aliases []string) error {
	fmt.Println("Inside docker network connect: ", id, container)

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	err := c.client.NetworkConnect(ctx, id, container, &dockernetworktypes.EndpointSettings{})
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	return nil
}

func (c *dockerClient) NetworkDisconnect(id string, container string, force bool) error {
	fmt.Println("Inside docker network disconnect: ", id, container)

	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	err := c.client.NetworkDisconnect(ctx, id, container, force)
	if ctxErr := contextError(ctx); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return err
	}
	return nil
}

func (c *dockerClient) ContainerExec(id string, cmd []string) (driver.ExecResult, error) {
	fmt.Println("Inside docker container exec")
	ctx, cancel := getTimeoutContext(&Driver)
	defer cancel()

	execConfig := dockertypes.ExecConfig{
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          cmd,
	}

	createResponse, err := c.client.ContainerExecCreate(ctx, id, execConfig)
	if err != nil {
		return driver.ExecResult{}, err
	}
	execID := createResponse.ID

	// run with stdout and stderr attached
	attachResponse, err := c.client.ContainerExecAttach(ctx, execID, dockertypes.ExecStartCheck{})
	if err != nil {
		return driver.ExecResult{}, err
	}
	defer attachResponse.Close()

	var outBuf, errBuf bytes.Buffer
	outputDone := make(chan error, 1)

	go func() {
		_, err = dockerstdcopy.StdCopy(&outBuf, &errBuf, attachResponse.Reader)
		outputDone <- err
	}()

	select {
	case err := <-outputDone:
		if err != nil {
			return driver.ExecResult{}, err
		}
		break
	}

	inspectResponse, err := c.client.ContainerExecInspect(ctx, execID)
	if err != nil {
		return driver.ExecResult{}, err
	}

	return driver.ExecResult{ExitCode: inspectResponse.ExitCode, OutBuffer: &outBuf, ErrBuffer: &errBuf}, nil
}
