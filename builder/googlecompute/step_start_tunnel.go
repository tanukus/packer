package googlecompute

import (
	"os/exec"

	"github.com/hashicorp/packer/helper/communicator"
)

// StepStartTunnel represents a Packer build step that launches an IAP tunnel
type StepStartTunnel struct {
	IAP      bool
	commConf *communicator.Config
}

// Run executes the Packer build step that creates an IAP tunnel.
func (s *StepStartTunnel) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	// Just to try to get a POC working, shell out to create the tunnel. Hopefully
	// We can replace this with legit google SDK calls later.
	args = []string{"compute", "start-iap-tunnel", instance, port}
	cmd := exec.Command("gcloud", args...)
	return multistep.ActionContinue
}

// Cleanup destroys the GCE instance created during the image creation process.
func (s *StepStartTunnel) Cleanup(state multistep.StateBag) {
	return
}
