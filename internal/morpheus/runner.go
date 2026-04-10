package morpheus

import "os/exec"

// CommandRunner abstracts exec.Command for testing.
// Extracted from internal/athena to keep morpheus self-contained.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
}

// defaultCommandRunner uses os/exec.
type defaultCommandRunner struct{}

func (r *defaultCommandRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// DefaultCommandRunner returns the real exec-based runner.
func DefaultCommandRunner() CommandRunner {
	return &defaultCommandRunner{}
}
