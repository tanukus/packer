package googlecompute

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/packer/common/net"
	"github.com/hashicorp/packer/helper/communicator"
	"github.com/hashicorp/packer/helper/multistep"
	"github.com/hashicorp/packer/packer"
	"github.com/hashicorp/packer/packer/tmp"
)

// StepStartTunnel represents a Packer build step that launches an IAP tunnel
type IAPConfig struct {
	// Whether to use an IAP proxy.
	IAP bool `mapstructure:"use_iap" required:"false"`
	// Which port to connect the other end of the IAM localhost proxy to, if you care.
	IAPLocalhostPort int `mapstructure:"iap_localhost_port"`
	// What "hashbang" to use to invoke script that sets up gcloud.
	// Default: "/bin/sh"
	IAPHashBang string `mapstructure:"iap_hashbang" required:"false"`
	// What file extension to use for script that sets up gcloud.
	// Default: ".sh"
	IAPExt string `mapstructure:"iap_ext" required:"false"`
}

type StepStartTunnel struct {
	IAPConf     *IAPConfig
	CommConf    *communicator.Config
	AccountFile string

	ctxCancel context.CancelFunc
}

func (s *StepStartTunnel) ConfigureLocalHostPort(ctx context.Context) error {
	if s.IAPConf.IAPLocalhostPort == 0 {
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

		s.IAPConf.IAPLocalhostPort = l.Port
		l.Close()
		log.Printf("Setting up proxy to listen on localhost at %d",
			s.IAPConf.IAPLocalhostPort)
	}
	return nil
}

func (s *StepStartTunnel) createTempGcloudScript(args []string) (string, error) {
	// Generate temp script that contains both gcloud auth and gcloud compute
	// iap launch call.

	// Create temp file.
	tf, err := tmp.File("gcloud-setup")
	if err != nil {
		return "", fmt.Errorf("Error preparing gcloud setup script: %s", err)
	}
	defer tf.Close()
	// Write our contents to it
	writer := bufio.NewWriter(tf)

	s.IAPConf.IAPHashBang = fmt.Sprintf("#!%s\n", s.IAPConf.IAPHashBang)
	log.Printf("[INFO] (google): Prepending inline gcloud setup script with %s",
		s.IAPConf.IAPHashBang)
	writer.WriteString(s.IAPConf.IAPHashBang)

	// authenticate to gcloud
	_, err = writer.WriteString(
		fmt.Sprintf("gcloud auth activate-service-account --key-file='%s'\n",
			s.AccountFile))
	if err != nil {
		return "", fmt.Errorf("Error preparing gcloud shell script: %s", err)
	}
	// call command
	args = append([]string{"gcloud"}, args...)
	argString := strings.Join(args, " ")
	if _, err := writer.WriteString(argString + "\n"); err != nil {
		return "", fmt.Errorf("Error preparing gcloud shell script: %s", err)
	}

	if err := writer.Flush(); err != nil {
		return "", fmt.Errorf("Error preparing shell script: %s", err)
	}

	err = os.Chmod(tf.Name(), 0700)
	if err != nil {
		log.Printf("[ERROR] (google): error modifying permissions of temp script file: %s", err.Error())
	}

	// figure out what extension the file should have, and rename it.
	tempScriptFileName := tf.Name()
	if s.IAPConf.IAPExt != "" {
		os.Rename(tempScriptFileName, fmt.Sprintf("%s%s", tempScriptFileName, s.IAPConf.IAPExt))
		tempScriptFileName = fmt.Sprintf("%s%s", tempScriptFileName, s.IAPConf.IAPExt)
	}

	log.Printf("Megan tempfilename is %s", tempScriptFileName)

	return tempScriptFileName, nil
}

// Run executes the Packer build step that creates an IAP tunnel.
func (s *StepStartTunnel) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	if !s.IAPConf.IAP {
		log.Printf("Skipping step launch IAP tunnel; \"iap\" is false.")
		return multistep.ActionContinue
	}

	// shell out to create the tunnel.
	ui := state.Get("ui").(packer.Ui)
	instanceName := state.Get("instance_name").(string)
	c := state.Get("config").(*Config)

	ui.Say("Step Launch IAP Tunnel...")

	err := s.ConfigureLocalHostPort(ctx)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Generate list of args to use to call gcloud cli.
	args := []string{"compute", "start-iap-tunnel", instanceName,
		strconv.Itoa(s.CommConf.Port()),
		fmt.Sprintf("--local-host-port=localhost:%d", s.IAPConf.IAPLocalhostPort),
		"--zone", c.Zone,
	}

	// Update SSH config to use localhost proxy instead, using the proxy
	// settings.
	if s.CommConf.Type == "ssh" {
		s.CommConf.SSHHost = "localhost"
		// this is the port the IAP tunnel listens on, on localhost.
		// TODO make setting LocalHostPort optional
		s.CommConf.SSHPort = s.IAPConf.IAPLocalhostPort
	} else {
		err := fmt.Errorf("Error: IAP tunnel currently only implemnted for" +
			" SSH communicator")
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	log.Printf("Calling tunnel launch with args %#v", args)

	// Create temp file that contains both gcloud authentication, and gcloud
	// proxy setup call.
	tempScriptFileName, err := s.createTempGcloudScript(args)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
	// defer os.Remove(tempScriptFileName)

	// Shell out to gcloud.
	cancelCtx, cancel := context.WithCancel(ctx)
	s.ctxCancel = cancel

	// set stdout and stderr so we can read what's going on.
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.CommandContext(cancelCtx, tempScriptFileName)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Start()
	log.Printf("Waiting 30s for tunnel to create...")
	time.Sleep(30 * time.Second)
	if err != nil {
		err := fmt.Errorf("Error calling gcloud sdk to launch IAP tunnel: %s",
			err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
	log.Printf("Megan stdout is:")
	log.Println(stdout.String())
	log.Printf("Megan stderr is:")
	log.Println(stderr.String())

	// TODO error check tunnel launch success

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
