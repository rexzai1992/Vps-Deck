package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CommandRunner executes a single shell command and returns combined output and exit code.
// All implementations must enforce an allowlist — no arbitrary shell execution.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (output string, exitCode int, err error)
}

// allowedSubcommands maps binary names to the first-arg prefixes they may be called with.
// Repair actions (reload, restart) are only callable via Repair(), not Run() from checks.
var allowedSubcommands = map[string][]string{
	"systemctl": {"is-active", "status"},
	"nginx":     {"-t", "--version"},
	"ufw":       {"status"},
	"docker":    {"info", "version"},
}

var allowedRepairCommands = map[RepairType][]string{
	RepairNginxReload:   {"systemctl", "reload", "nginx"},
	RepairNginxRestart:  {"systemctl", "restart", "nginx"},
	RepairDockerRestart: {"systemctl", "restart", "docker"},
}

// RealRunner executes commands on the host OS within the allowlist.
type RealRunner struct{}

func (r *RealRunner) Run(ctx context.Context, name string, args ...string) (string, int, error) {
	allowed, ok := allowedSubcommands[name]
	if !ok {
		return "", -1, fmt.Errorf("command not in allowlist: %q", name)
	}
	if len(args) > 0 {
		found := false
		for _, a := range allowed {
			if a == args[0] {
				found = true
				break
			}
		}
		if !found {
			return "", -1, fmt.Errorf("subcommand not allowed: %q %q", name, args[0])
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, _ := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return strings.TrimSpace(string(out)), code, nil
}

// RunRepair executes a pre-approved repair action.
// It deliberately does not use Run() so that repair verbs (reload/restart) stay
// out of the read-only diagnostic allowlist.
func (r *RealRunner) RunRepair(ctx context.Context, rt RepairType) (string, error) {
	cmdArgs, ok := allowedRepairCommands[rt]
	if !ok {
		return "", fmt.Errorf("unknown repair type: %q", rt)
	}
	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// FakeResult is a canned response for a specific command invocation.
type FakeResult struct {
	Output   string
	ExitCode int
}

// FakeRunner returns pre-configured responses; used only in tests.
// Key format: "name arg1 arg2 ..."
type FakeRunner struct {
	Results      map[string]FakeResult
	RepairOutput map[RepairType]string
	RepairErr    map[RepairType]error
}

func (f *FakeRunner) Run(_ context.Context, name string, args ...string) (string, int, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	if r, ok := f.Results[key]; ok {
		return r.Output, r.ExitCode, nil
	}
	return "", 0, nil
}

func (f *FakeRunner) RunRepair(_ context.Context, rt RepairType) (string, error) {
	out := ""
	if f.RepairOutput != nil {
		out = f.RepairOutput[rt]
	}
	var err error
	if f.RepairErr != nil {
		err = f.RepairErr[rt]
	}
	return out, err
}

// Repairer is the interface used by Service.Repair; both RealRunner and FakeRunner implement it.
type Repairer interface {
	RunRepair(ctx context.Context, rt RepairType) (string, error)
}
