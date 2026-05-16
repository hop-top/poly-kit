package uxp

import (
	"os"
	"os/exec"
	"sort"
)

// CheckStatus represents the result status of a diagnostic check.
type CheckStatus int

const (
	StatusOK CheckStatus = iota
	StatusWarn
	StatusFail
	StatusSkip
)

// Check is the result of a single diagnostic check.
type Check struct {
	Name    string
	Status  CheckStatus
	Message string
	Detail  string
}

// CheckFunc is a function that performs a diagnostic check.
type CheckFunc func() Check

// CommandRunner executes a named program and returns any error.
// A nil CommandRunner uses exec.LookPath as default.
type CommandRunner func(name string, args ...string) error

// Doctor runs diagnostic checks.
type Doctor struct {
	checks []CheckFunc
}

// NewDoctor creates a new Doctor.
func NewDoctor() *Doctor {
	return &Doctor{}
}

// Add appends a check function.
func (d *Doctor) Add(fn CheckFunc) {
	d.checks = append(d.checks, fn)
}

// Run executes all checks and returns results sorted by severity
// (fails first, then warns, then ok, then skip).
func (d *Doctor) Run() []Check {
	if len(d.checks) == 0 {
		return nil
	}

	results := make([]Check, len(d.checks))
	for i, fn := range d.checks {
		results[i] = fn()
	}

	sort.SliceStable(results, func(i, j int) bool {
		return severityRank(results[i].Status) < severityRank(results[j].Status)
	})

	return results
}

// severityRank maps CheckStatus to a sort key where higher severity
// (fail) sorts first.
func severityRank(s CheckStatus) int {
	switch s {
	case StatusFail:
		return 0
	case StatusWarn:
		return 1
	case StatusOK:
		return 2
	case StatusSkip:
		return 3
	default:
		return 4
	}
}

// CheckCLIInstalled returns a CheckFunc that verifies the CLI binary
// is found in PATH. If runner is nil, exec.LookPath is used.
func CheckCLIInstalled(cli CLIName, runner CommandRunner) CheckFunc {
	return func() Check {
		name := string(cli)
		if runner != nil {
			err := runner(name)
			if err != nil {
				return Check{
					Name:    name + "-installed",
					Status:  StatusFail,
					Message: name + " not found",
					Detail:  err.Error(),
				}
			}
			return Check{
				Name:    name + "-installed",
				Status:  StatusOK,
				Message: name + " found",
			}
		}

		// Default: use exec.LookPath.
		path, err := exec.LookPath(name)
		if err != nil {
			return Check{
				Name:    name + "-installed",
				Status:  StatusFail,
				Message: name + " not found in PATH",
				Detail:  err.Error(),
			}
		}
		return Check{
			Name:    name + "-installed",
			Status:  StatusOK,
			Message: name + " found at " + path,
		}
	}
}

// CheckStoreExists returns a CheckFunc that verifies the CLI's
// resolved store root directory exists on disk.
func CheckStoreExists(cli CLIName, reg *CLIRegistry) CheckFunc {
	return func() Check {
		name := string(cli)
		p, err := ResolveStorePath(cli, reg)
		if err != nil {
			return Check{
				Name:    name + "-store",
				Status:  StatusFail,
				Message: name + " store path resolution failed",
				Detail:  err.Error(),
			}
		}

		fi, err := os.Stat(p)
		if err != nil {
			status := StatusWarn
			msg := name + " store not found"
			if !os.IsNotExist(err) {
				status = StatusFail
				msg = name + " store inaccessible"
			}
			return Check{
				Name:    name + "-store",
				Status:  status,
				Message: msg,
				Detail:  p + ": " + err.Error(),
			}
		}
		if !fi.IsDir() {
			return Check{
				Name:    name + "-store",
				Status:  StatusFail,
				Message: name + " store path is not a directory",
				Detail:  p,
			}
		}

		return Check{
			Name:    name + "-store",
			Status:  StatusOK,
			Message: name + " store exists",
			Detail:  p,
		}
	}
}
