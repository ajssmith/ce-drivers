package driver

import (
	"bytes"
	"time"
)

const (
	// defaultTimeout is the default timeout of short running docker operations.
	// Value is slightly offset from 2 minutes to make timeouts due to this
	// constant recognizable.
	DefaultTimeout = 2*time.Minute - 1*time.Second

	// defaultShmSize is the default ShmSize to use (in bytes) if not specified.
	DefaultShmSize = int64(1024 * 1024 * 64)

	// defaultImagePullingProgressReportInterval is the default interval of image pulling progress reporting.
	DefaultImagePullingProgressReportInterval = 10 * time.Second
)

type ContainerStatus int

type Driver interface {
	New() error
	ImageInspect(id string) (*ImageInspect, error)
	ImagesList(options ImageListOptions) ([]ImageSummary, error)
	ImagesPull(refStr string, options ImagePullOptions) ([]string, error)
	ContainerCreate(image string) (ContainerCreateResponse, error)
	ContainerStart(id string) error
	ContainerWait(id string, state string, timeout time.Duration, interval time.Duration) error
	ContainerList(options ContainerListOptions) ([]Container, error)
	ContainerInspect(id string) (*InspectContainerData, error)
	ContainerStop(id string) error
	ContainerRemove(id string) error
	ContainerExec(id string, cmd []string) (ExecResult, error)
	NetworkCreate(name string, options NetworkCreateOptions) (NetworkCreateResponse, error)
	NetworkInspect(id string) (NetworkResource, error)
	NetworkRemove(id string) error
	NetworkConnect(id string, container string, aliases []string) error
	NetworkDisconnect(id string, container string, force bool) error
}

// TODO: add Config
type ImageInspect struct {
	ID       string   `json:"Id"`
	Created  int64    `json:"Created"`
	RepoTags []string `json:",omitempty"`
	Size     int64    `json:"Size"`
}

type ImagePullOptions struct {
	All bool
}

type ImageListOptions struct {
	All bool
}

type ContainerListOptions struct {
	All bool
}

type NetworkListOptions struct {
	All bool
}

// TODO: podman Image has container config, where should this come from
type ImageSummary struct {
	ID          string            `json:"Id"`
	Created     int64             `json:"Created"`
	Labels      map[string]string `json:"Labels"`
	RepoTags    []string          `json:",omitempty"`
	RepoDigests []string          `json:",omitempty"`
	Size        int64             `json:"Size"`
}

type ContainerCreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type Container struct {
	ID      string `json:"Id"`
	Names   []string
	Image   string
	ImageID string
	Command string
	Created int64
	Ports   []Port
	Labels  map[string]string
	State   string
	Status  string
	Mounts  []MountPoint
}

type NetworkResource struct {
	Name string
}

// NOTE: ContainerJSONBase    for docker
//       InspectContainerData for podman
type InspectContainerData struct {
	// Base
	ID        string          `json:"Id"`
	Created   time.Time       `json:"Created"`
	Path      string          `json:"Path"`
	Args      []string        `json:"Args"`
	State     *ContainerState `json:"State"`
	Image     string          `json:"Image"`
	ImageName string          `json:"ImageName"`
	Name      string          `json:"Name"`
	Mounts    []MountPoint
	// Config
	// NetworkSettings
}

type MountPoint struct {
	Type        MountType `json:",omitempty"`
	Name        string    `json:",omitempty"`
	Source      string
	Destination string
	Driver      string `json:"omitempty"`
	Mode        string
	RW          bool
	Propagation MountPropagation
}

type MountType string

const (
	TypeBind      MountType = "bind"
	TypeVolume    MountType = "volume"
	TypeTmpfs     MountType = "tmpsfs"
	TypeNamedPipe MountType = "npipe"
)

type MountPropagation string

// TODO all propagation consts
const (
	PropagationRPrivate MountPropagation = "rprivate"
	PropagationPrivate  MountPropagation = "private"
	PropagationRShared  MountPropagation = "rshared"
	PropagationShared   MountPropagation = "shared"
)

type Port struct {
	IP            string `json: "IP,omitempty"`
	ContainerPort uint16 `json:"ContainerPort"`
	HostPort      uint16 `json:"HostPort,omitempty"`
	Type          string `json:"Type"`
}

type ContainerState struct {
	Status  string
	Running bool
	Paused  bool
}

type NetworkCreateOptions struct {
	CheckDuplicate bool
	Driver         string
	Options        map[string]string
	Labels         map[string]string
}

type NetworkCreateResponse struct {
	ID      string `json:"Id"`
	Warning string
}

type ExecResult struct {
	ExitCode  int
	OutBuffer *bytes.Buffer
	ErrBuffer *bytes.Buffer
}

func (res *ExecResult) Stderr() string {
	return res.ErrBuffer.String()
}

func (res *ExecResult) Stdout() string {
	return res.OutBuffer.String()
}
