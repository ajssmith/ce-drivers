package main

import (
	"fmt"
	"os"
	"plugin"
	"time"

	"github.com/ajssmith/ce-drivers/driver"
)

//type Driver interface {
//	New() error
//}

func main() {
	var (
		err error
		p   *plugin.Plugin
		//		n   plugin.Symbol
	)

	if len(os.Args) != 2 {
		fmt.Println("usage run cmd/main.go drivername")
		os.Exit(1)
	}

	name := os.Args[1]
	module := fmt.Sprintf("./%s.so", name)
	fmt.Println("module: ", module)
	_, err = os.Stat(module)
	if os.IsNotExist(err) {
		fmt.Println("Can't find a driver named:", name)
		os.Exit(1)
	}

	if p, err = plugin.Open(module); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	symDriver, err := p.Lookup("Driver")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	var drv driver.Driver
	drv, ok := symDriver.(driver.Driver)
	if !ok {
		fmt.Println("That is not a driver")
		os.Exit(1)
	}
	drv.New()

	_, err = drv.ImagesPull("quay.io/skupper/qdrouterd:0.4", driver.ImagePullOptions{})
	if err != nil {
		fmt.Println(err)
	}

	imageSummary, err := drv.ImagesList(driver.ImageListOptions{})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	var names []string
	for _, i := range imageSummary {
		names = append(names, i.RepoTags...)
	}
	fmt.Println("Listing images...")
	for _, n := range names {
		fmt.Println(n)
	}

	fmt.Println("Inspecting image")
	imageData, err := drv.ImageInspect("quay.io/skupper/qdrouterd:0.4")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("Image data ", imageData)

	fmt.Println("Creating Container")
	resp, err := drv.ContainerCreate("quay.io/skupper/qdrouterd:0.4")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Starting Container")
	err = drv.ContainerStart(resp.ID)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Wait for container to be running")
	err = drv.ContainerWait(resp.ID, "running", time.Second*30, time.Second*5)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Let's get list of containers")
	containerList, err := drv.ContainerList(driver.ContainerListOptions{})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("Latest container is %s and state is %s \n", containerList[0].Names[0], containerList[0].State)

	fmt.Println("Lets inpect the container shall we")
	ci, err := drv.ContainerInspect(resp.ID)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("The container name is %s and uses image %s\n", ci.Name, ci.ImageName)

	fmt.Println("Let's create a network")
	_, err = drv.NetworkCreate("skupper-network", driver.NetworkCreateOptions{})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("And inspect the network")
	ni, err := drv.NetworkInspect("skupper-network")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("The network name is", ni.Name)

	fmt.Println("Connect container to network")
	err = drv.NetworkConnect("skupper-network", resp.ID, []string{})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Exec a command")
	execResult, err := drv.ContainerExec(resp.ID, []string{"qdstat", "-g"})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("exec output: ", execResult.Stdout())

	fmt.Println("Exec a second command")
	execResult, err = drv.ContainerExec(resp.ID, []string{"qdstat", "-l"})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Println("exec second output: ", execResult.Stdout())

	fmt.Println("Disconnect container from network")
	err = drv.NetworkDisconnect("skupper-network", resp.ID, true)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("Now let's stop the container")
	err = drv.ContainerStop(resp.ID)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	//	fmt.Println("Let's get another list of containers")
	//	containerList, err = drv.ContainerList(driver.ContainerListOptions{})
	//	if err != nil {
	//		fmt.Println(err)
	//		os.Exit(1)
	//	}
	//	fmt.Printf("Latest container is %s and state is %s \n", containerList[0].Names[0], containerList[0].State)

	fmt.Println("Remove the container")
	err = drv.ContainerRemove(resp.ID)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	fmt.Println("And remove the network too")
	err = drv.NetworkRemove("skupper-network")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
