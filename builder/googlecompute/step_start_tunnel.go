package googlecompute

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"

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

// Run executes the Packer build step that creates an IAP tunnel.
func (s *StepStartTunnel) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	if !s.IAP {
		log.Printf("Skipping step launch IAP tunnel; s.IAP is false.")
		return multistep.ActionContinue
	}

	// shell out to create the tunnel.
	ui := state.Get("ui").(packer.Ui)
	instanceName := state.Get("instance_name").(string)

	ui.Say("Step Launch IAP Tunnel...")

	// Update SSH config to use localhost proxy instead, using the proxy
	// settings.
	if s.CommConf.Type == "ssh" {
		s.CommConf.SSHProxyHost = "127.0.0.1"
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
		strconv.Itoa(s.CommConf.Port())}

	// User must define localhost port to use; TODO let google cli figure it out.
	args = append(args, fmt.Sprintf("--local-host-port=localhost:%d",
		s.LocalHostPort))

	log.Printf("Calling tunnel launch with args %#v", args)

	cancelCtx, cancel := context.WithCancel(ctx)
	s.ctxCancel = cancel
	cmd := exec.CommandContext(cancelCtx, "gcloud", args...)
	err := cmd.Start()
	if err != nil {
		err := fmt.Errorf("Error calling gcloud sdk to launch IAP tunnel: %s",
			err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
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
