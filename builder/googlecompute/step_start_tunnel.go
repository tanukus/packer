package googlecompute

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"time"

	"github.com/hashicorp/packer/common/net"
	"github.com/hashicorp/packer/helper/communicator"
	"github.com/hashicorp/packer/helper/multistep"
	"github.com/hashicorp/packer/packer"
)

// StepStartTunnel represents a Packer build step that launches an IAP tunnel
type StepStartTunnel struct {
	IAP           bool
	CommConf      *communicator.Config
	LocalHostPort int

	ctxCancel context.CancelFunc
}

func (s *StepStartTunnel) ConfigureLocalHostPort() error {
	if s.LocalHostPort == 0 {
		log.Printf("Finding an available TCP port for IAP proxy")
		l, err := net.ListenRangeConfig{
			Min:     8000,
			Max:     9000,
			Addr:    "0.0.0.0",
			Network: "tcp",
		}.Listen(ctx)

		if err != nil {
			err := fmt.Errorf("error finding an available port to initiate a session tunnel: %s", err)
			return err
		}

		s.LocalHostPort = l.Port
		l.Close()
		log.Printf("Setting up proxy to listen on localhost at %d",
			s.LocalHostPort)
	}
	return nil
}

func (s *StepStartTunnel) ModifyProxyConfig() {

}

// Run executes the Packer build step that creates an IAP tunnel.
func (s *StepStartTunnel) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	if !s.IAP {
		log.Printf("Skipping step launch IAP tunnel; s.IAP is false.")
		return multistep.ActionContinue
	}

	// shell out to create the tunnel.
	ui := state.Get("ui").(packer.Ui)
	instanceName := state.Get("instance_name").(string)
	c := state.Get("config").(*Config)

	ui.Say("Step Launch IAP Tunnel...")

	err := s.ConfigureLocalHostPort()
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Update SSH config to use localhost proxy instead, using the proxy
	// settings.
	if s.CommConf.Type == "ssh" {
		s.CommConf.SSHProxyHost = "localhost"
		// this is the port the IAP tunnel listens on, on localhost.
		// TODO make setting LocalHostPort optional
		s.CommConf.SSHProxyPort = s.LocalHostPort
	} else {
		err := fmt.Errorf("Error: IAP tunnel currently only implemnted for" +
			" SSH communicator")
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Generate list of args to use to call gcloud cli.
	args := []string{"compute", "start-iap-tunnel", instanceName,
		strconv.Itoa(s.CommConf.Port()),
		fmt.Sprintf("--local-host-port=localhost:%d", s.LocalHostPort),
		"--zone", c.Zone,
	}

	log.Printf("Calling tunnel launch with args %#v", args)

	cancelCtx, cancel := context.WithCancel(ctx)
	s.ctxCancel = cancel

	// set stdout and stderr so we can read what's going on.
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.CommandContext(cancelCtx, "gcloud", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Start()
	log.Printf("Waiting 30s for tunnel to create...")
	time.Sleep(30 * time.Second)
	if err != nil {
		err := fmt.Errorf("Error calling gcloud sdk to launch IAP tunnel: %s",
			err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
	log.Println(stdout.String())
	log.Println(stderr.String())

	return multistep.ActionContinue
}

// Cleanup destroys the GCE instance created during the image creation process.
func (s *StepStartTunnel) Cleanup(state multistep.StateBag) {
	// close the tunnel by cancelling the context used to launch it.
	if s.ctxCancel != nil {
		s.ctxCancel()
	}
	return
}
