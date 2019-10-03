package main

import (
	"os"
	"time"

	"github.com/Azure/azure-docker-extension/pkg/vmextension"
	"github.com/go-kit/kit/log"
	"github.com/pkg/errors"
)

type cmdFunc func(ctx *log.Context, hEnv vmextension.HandlerEnvironment, seqNum int) (msg string, err error)
type preFunc func(ctx *log.Context, seqNum int) error

type cmd struct {
	f                  cmdFunc // associated function
	name               string  // human readable string
	shouldReportStatus bool    // determines if running this should log to a .status file
	pre                preFunc // executed before any status is reported
	failExitCode       int     // exitCode to use when commands fail
}

const (
	fullName = "Microsoft.ManagedServices.ApplicationHealthLinux"
)

var (
	cmdInstall   = cmd{install, "Install", false, nil, 52}
	cmdEnable    = cmd{enable, "Enable", true, nil, 3}
	cmdUninstall = cmd{uninstall, "Uninstall", false, nil, 3}

	cmds = map[string]cmd{
		"install":   cmdInstall,
		"uninstall": cmdUninstall,
		"enable":    cmdEnable,
		"update":    {noop, "Update", true, nil, 3},
		"disable":   {noop, "Disable", true, nil, 3},
	}
)

func noop(ctx *log.Context, h vmextension.HandlerEnvironment, seqNum int) (string, error) {
	ctx.Log("event", "noop")
	return "", nil
}

func install(ctx *log.Context, h vmextension.HandlerEnvironment, seqNum int) (string, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create data dir")
	}

	ctx.Log("event", "created data dir", "path", dataDir)
	ctx.Log("event", "installed")
	return "", nil
}

func uninstall(ctx *log.Context, h vmextension.HandlerEnvironment, seqNum int) (string, error) {
	{ // a new context scope with path
		ctx = ctx.With("path", dataDir)
		ctx.Log("event", "removing data dir", "path", dataDir)
		if err := os.RemoveAll(dataDir); err != nil {
			return "", errors.Wrap(err, "failed to delete data dir")
		}
		ctx.Log("event", "removed data dir")
	}
	ctx.Log("event", "uninstalled")
	return "", nil
}

var (
	stateChangeLogMap = map[HealthStatus]string{
		Healthy:   "state changed to healthy",
		Unhealthy: "state changed to unhealthy",
	}

	healthStatusToStatusType = map[HealthStatus]StatusType{
		Healthy:   StatusSuccess,
		Unhealthy: StatusError,
	}

	healthStatusToMessage = map[HealthStatus]string{
		Healthy:   "Application found to be healthy",
		Unhealthy: "Application found to be unhealthy",
	}
)

const (
	statusMessage = "Successfully polling for application health"
	substatusName = "AppHealthStatus"
)

var (
	errTerminated = errors.New("Application health process terminated")
)

func enable(ctx *log.Context, h vmextension.HandlerEnvironment, seqNum int) (string, error) {
	// parse the extension handler settings (not available prior to 'enable')
	cfg, err := parseAndValidateSettings(ctx, h.HandlerEnvironment.ConfigFolder)
	if err != nil {
		return "", errors.Wrap(err, "failed to get configuration")
	}

	var prevState HealthStatus
	probe := NewHealthProbe(ctx, &cfg)

    var (
        intervalInSeconds = cfg.intervalInSeconds()
        numberOfProbes = cfg.numberOfProbes()
    )

    var (
        numOfConsecutiveUnhealthyProbes = 0
    )

	for {
		state, err := probe.evaluate(ctx)
		if err != nil {
			return "", errors.Wrap(err, "failed to evaluate health")
		}

		if shutdown {
			return "", errTerminated
		}

		if prevState != state {
			ctx.Log("event", stateChangeLogMap[state])
			prevState = state
			
            // Consecutive Unhealthy probe count will need to be reset
            numOfConsecutiveUnhealthyProbes = 0
        }
        
        if state == Unhealthy {
            // Current and previous state is Unhealthy. So increment consecutive unhealthy probes
            // count, capped to "numberOfProbes" configuration
            if numOfConsecutiveUnhealthyProbes < numberOfProbes {
                numOfConsecutiveUnhealthyProbes++
            }
        }

        // If consecutive unhealth probes match (or exceed) "numberOfProbes" config, mark current
        // state as Unhealthy. Otherwise current state is Healthy
        derivedState := Healthy
        if numOfConsecutiveUnhealthyProbes == numberOfProbes {
            derivedState = Unhealthy
        }

        reportStatusWithSubstatus(ctx, h, seqNum, StatusSuccess, "enable", statusMessage, healthStatusToStatusType[derivedState], substatusName, healthStatusToMessage[derivedState])
        time.Sleep(intervalInSeconds * time.Second)

        if shutdown {
            return "", errTerminated
        }
    }
}
