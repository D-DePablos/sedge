package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NethermindEth/1click/configs"
	"github.com/NethermindEth/1click/internal/pkg/clients"
	"github.com/NethermindEth/1click/internal/pkg/commands"
	"github.com/NethermindEth/1click/internal/utils"
	posmoni "github.com/NethermindEth/posmoni/pkg/eth2"
	"github.com/manifoldco/promptui"
)

// Interface for Posmoni Eth2 monitor
type MonitoringTool interface {
	TrackSync(done <-chan struct{}, beaconEndpoints, executionEndpoints []string, wait time.Duration) <-chan posmoni.EndpointSyncStatus
}

var monitor MonitoringTool

func initMonitor(builder func() MonitoringTool) {
	monitor = builder()
}

func installOrShowInstructions(pending []string) (err error) {
	// notest
	optInstall, optExit := "Install dependencies", "Exit. You will manage this dependencies on your own"
	prompt := promptui.Select{
		Label: "Select how to proceed with the pending dependencies",
		Items: []string{optInstall, optExit},
	}

	if err = utils.HandleInstructions(pending, utils.ShowInstructions); err != nil {
		return fmt.Errorf(configs.ShowingInstructionsError, err)
	}
	_, result, err := prompt.Run()
	if err != nil {
		return fmt.Errorf(configs.PromptFailedError, err)
	}

	switch result {
	case optInstall:
		return installDependencies(pending)
	default:
		log.Info(configs.Exiting)
		os.Exit(0)
	}

	return nil
}

func installDependencies(pending []string) error {
	if err := utils.HandleInstructions(pending, utils.InstallDependency); err != nil {
		return fmt.Errorf(configs.InstallingDependenciesError, err)
	}
	return nil
}

func randomizeClients(allClients clients.OrderedClients) (clients.Clients, error) {
	var executionClient, consensusClient clients.Client
	var combinedClients clients.Clients

	executionClient, err := clients.RandomChoice(allClients[execution])
	if err != nil {
		return combinedClients, err
	}
	consensusClient, err = clients.RandomChoice(allClients[consensus])
	if err != nil {
		return combinedClients, err
	}

	combinedClients = clients.Clients{
		Execution: executionClient,
		Consensus: consensusClient,
		Validator: consensusClient}
	return combinedClients, nil
}

func validateClients(allClients clients.OrderedClients, w io.Writer) (clients.Clients, error) {
	var combinedClients clients.Clients
	var err error

	// Select a random execution client, consensus client and validator client
	randomizedClients, err := randomizeClients(allClients)
	if err != nil {
		return combinedClients, err
	}

	// Randomize missing clients, and choose same pair of client for consensus and validator if at least one of them is missing
	if executionName == "" {
		log.Warnf(configs.ExecutionClientNotSpecifiedWarn, randomizedClients.Execution.Name)
		executionName = randomizedClients.Execution.Name
	}
	if consensusName == "" && validatorName == "" {
		log.Warnf(configs.CLNotSpecifiedWarn, randomizedClients.Consensus.Name)
		consensusName = randomizedClients.Consensus.Name
		validatorName = randomizedClients.Validator.Name
	} else if consensusName == "" {
		log.Warn(configs.ConsensusClientNotSpecifiedWarn)
		consensusName = validatorName
	} else if validatorName == "" {
		log.Warn(configs.ValidatorClientNotSpecifiedWarn)
		validatorName = consensusName
	}

	exec, ok := allClients[execution][executionName]
	if !ok {
		exec.Name = executionName
	}
	cons, ok := allClients[consensus][consensusName]
	if !ok {
		cons.Name = consensusName
	}
	val, ok := allClients[validator][validatorName]
	if !ok {
		val.Name = validatorName
	}

	combinedClients = clients.Clients{
		Execution: exec,
		Consensus: cons,
		Validator: val,
	}

	if err = clients.ValidateClient(combinedClients.Execution, execution); err != nil {
		return combinedClients, err
	}
	if err = clients.ValidateClient(combinedClients.Consensus, consensus); err != nil {
		return combinedClients, err
	}
	if err = clients.ValidateClient(combinedClients.Validator, validator); err != nil {
		return combinedClients, err
	}

	return combinedClients, nil
}

func runScriptOrExit() (err error) {
	// notest
	optRun, optExit := fmt.Sprintf("Run the script with the selected services %s", strings.Join(*services, ",")), "Exit"
	prompt := promptui.Select{
		Label: "Select how to proceed with the generated docker-compose script",
		Items: []string{optRun, optExit},
	}

	log.Infof(configs.InstructionsFor, "running docker-compose script")
	upCMD := commands.Runner.BuildDockerComposeUpCMD(commands.DockerComposeUpOptions{
		Path:     generationPath,
		Services: *services,
	})
	fmt.Printf("\n%s\n\n", upCMD.Cmd)

	_, result, err := prompt.Run()
	if err != nil {
		return fmt.Errorf("prompt failed %s", err)
	}

	switch result {
	case optRun:
		if err = runAndShowContainers(*services); err != nil {
			return err
		}
	default:
		log.Info(configs.Exiting)
		os.Exit(0)
	}

	return nil
}

func runAndShowContainers(services []string) error {
	// TODO: (refac) Put this check to checks.go and call it from there
	// Check if docker engine is on
	log.Info(configs.CheckingDockerEngine)
	psCMD := commands.Runner.BuildDockerPSCMD(commands.DockerPSOptions{
		All: true,
	})
	psCMD.GetOutput = true
	log.Infof(configs.RunningCommand, psCMD.Cmd)
	if _, err := commands.Runner.RunCMD(psCMD); err != nil {
		return fmt.Errorf(configs.DockerEngineOffError, err)
	}

	// Run docker-compose script
	upCMD := commands.Runner.BuildDockerComposeUpCMD(commands.DockerComposeUpOptions{
		Path:     filepath.Join(generationPath, configs.DefaultDockerComposeScriptName),
		Services: services,
	})
	log.Infof(configs.RunningCommand, upCMD.Cmd)
	if _, err := commands.Runner.RunCMD(upCMD); err != nil {
		return fmt.Errorf(configs.CommandError, upCMD.Cmd, err)
	}

	// Run docker-compose ps --filter status=running to show script running containers
	dcpsCMD := commands.Runner.BuildDockerComposePSCMD(commands.DockerComposePsOptions{
		Path:     filepath.Join(generationPath, configs.DefaultDockerComposeScriptName),
		Services: false,
	})
	log.Infof(configs.RunningCommand, dcpsCMD.Cmd)
	if _, err := commands.Runner.RunCMD(dcpsCMD); err != nil {
		return fmt.Errorf(configs.CommandError, dcpsCMD.Cmd, err)
	}

	return nil
}

func trackSync(m MonitoringTool, wait time.Duration) error {
	done := make(chan struct{})
	statuses := m.TrackSync(done, []string{configs.OnPremiseExecutionURL}, []string{configs.OnPremiseConsensusURL}, time.Minute)

	var esynced, csynced bool
	for s := range statuses {
		if s.Error != nil {
			return fmt.Errorf(configs.TrackSyncError, s.Endpoint, s.Error)
		}
		esynced = esynced || (s.Synced && s.Endpoint == configs.OnPremiseExecutionURL)
		csynced = csynced || (s.Synced && s.Endpoint == configs.OnPremiseConsensusURL)
		if esynced && csynced {
			// Stop tracking
			close(done)
			log.Info(configs.NodesSynced)
		}
	}

	return nil
}

func RunValidatorOrExit() error {
	// notest
	optRun, optExit := "Run validator service", "Exit"
	prompt := promptui.Select{
		Label: "Select how to proceed with the validator client",
		Items: []string{optRun, optExit},
	}

	log.Infof(configs.InstructionsFor, "running validator service of docker-compose script")
	upCMD := commands.Runner.BuildDockerComposeUpCMD(commands.DockerComposeUpOptions{
		Path:     generationPath,
		Services: []string{validator},
	})
	fmt.Printf("\n%s\n\n", upCMD.Cmd)

	_, result, err := prompt.Run()
	if err != nil {
		return fmt.Errorf("prompt failed %s", err)
	}

	switch result {
	case optRun:
		if err = runAndShowContainers([]string{validator}); err != nil {
			return err
		}
	default:
		log.Info(configs.Exiting)
		os.Exit(0)
	}

	return nil
}
