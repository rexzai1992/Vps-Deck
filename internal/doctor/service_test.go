package doctor

import (
	"context"
	"testing"
)

func newFake(results map[string]FakeResult) *FakeRunner {
	return &FakeRunner{Results: results}
}

func findCheck(checks []CheckResult, id string) (CheckResult, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return CheckResult{}, false
}

// makeService builds a Service with a FakeRunner and no DB (nil is safe when
// no project health checks are configured).
func makeService(runner *FakeRunner) *Service {
	return &Service{
		runner:    runner,
		repairer:  runner,
		panelPort: 8080,
		httpClient: nil, // no HTTP checks in runner-only tests
	}
}

func TestCheckNginxService_Active(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"systemctl is-active nginx": {Output: "active", ExitCode: 0},
	}))
	r := svc.checkNginxService(context.Background())
	if r.Status != StatusOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckNginxService_Inactive(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"systemctl is-active nginx": {Output: "inactive", ExitCode: 3},
	}))
	r := svc.checkNginxService(context.Background())
	if r.Status != StatusFailed {
		t.Errorf("expected Failed, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckNginxConfig_OK(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"nginx -t": {Output: "nginx: configuration file /etc/nginx/nginx.conf test is successful", ExitCode: 0},
	}))
	r := svc.checkNginxConfig(context.Background())
	if r.Status != StatusOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckNginxConfig_Error(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"nginx -t": {Output: "nginx: [emerg] unknown directive", ExitCode: 1},
	}))
	r := svc.checkNginxConfig(context.Background())
	if r.Status != StatusFailed {
		t.Errorf("expected Failed, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckDockerService_Active(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"systemctl is-active docker": {Output: "active", ExitCode: 0},
	}))
	r := svc.checkDockerService(context.Background())
	if r.Status != StatusOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckDockerService_Inactive(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"systemctl is-active docker": {Output: "inactive", ExitCode: 3},
	}))
	r := svc.checkDockerService(context.Background())
	if r.Status != StatusWarning {
		t.Errorf("expected Warning for inactive docker, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckFirewall_Active(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"ufw status": {Output: "Status: active\nTo                         Action      From\n--                         ------      ----\n22/tcp                     ALLOW       Anywhere", ExitCode: 0},
	}))
	r := svc.checkFirewall(context.Background())
	if r.Status != StatusOK {
		t.Errorf("expected OK, got %s: %s", r.Status, r.Message)
	}
}

func TestCheckFirewall_Inactive(t *testing.T) {
	svc := makeService(newFake(map[string]FakeResult{
		"ufw status": {Output: "Status: inactive", ExitCode: 0},
	}))
	r := svc.checkFirewall(context.Background())
	if r.Status != StatusWarning {
		t.Errorf("expected Warning for inactive ufw, got %s: %s", r.Status, r.Message)
	}
}

func TestAllowlistEnforced(t *testing.T) {
	runner := &RealRunner{}
	_, _, err := runner.Run(context.Background(), "rm", "-rf", "/")
	if err == nil {
		t.Error("expected error for disallowed command, got nil")
	}
}

func TestAllowlistSubcommandEnforced(t *testing.T) {
	runner := &RealRunner{}
	_, _, err := runner.Run(context.Background(), "systemctl", "stop", "nginx")
	if err == nil {
		t.Error("expected error for disallowed subcommand, got nil")
	}
}

func TestNewSummary(t *testing.T) {
	checks := []CheckResult{
		{Status: StatusOK},
		{Status: StatusOK},
		{Status: StatusWarning},
		{Status: StatusFailed},
		{Status: StatusSkipped},
	}
	s := newSummary(checks)
	if s.Total != 5 || s.OK != 2 || s.Warning != 1 || s.Failed != 1 || s.Skipped != 1 {
		t.Errorf("unexpected summary: %+v", s)
	}
}

func TestRepairAllowlist(t *testing.T) {
	fake := &FakeRunner{
		RepairOutput: map[RepairType]string{
			RepairNginxReload: "ok",
		},
	}
	svc := makeService(fake)
	result := svc.Repair(context.Background(), RepairNginxReload)
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
}

func TestRealRunnerRepairRejectsUnknownType(t *testing.T) {
	r := &RealRunner{}
	_, err := r.RunRepair(context.Background(), RepairType("drop_database"))
	if err == nil {
		t.Error("expected error for unknown repair type, got nil")
	}
}
