package engine

import (
	"context"

	"github.com/KHAEntertainment/self-updating-skills/internal/runner"
)

// AgentOptions configures the launchd agent install or uninstall. The
// zero value is usable: every path has a default derived from the
// environment, so the whole flow can be pointed at a temporary HOME.
type AgentOptions = runner.Options

// AgentResult is the outcome of an install or an uninstall.
type AgentResult = runner.Result

// AgentLink records one symlink the install created, repointed or left
// alone.
type AgentLink = runner.LinkChange

// InstallAgent writes the launchd plist, loads the job and — with the
// explicit Link flag — switches the ci-owned skills under ~/.agents/skills
// to symlinks into the repo and links every owned skill into every harness
// skills dir. The plist never holds a secret: the API key is read from the
// keychain through Enigma at run time.
func InstallAgent(ctx context.Context, opts AgentOptions) (*AgentResult, error) {
	return runner.Install(ctx, opts)
}

// UninstallAgent unloads the job, removes the plist and removes exactly
// the links the install created.
func UninstallAgent(ctx context.Context, opts AgentOptions) (*AgentResult, error) {
	return runner.Uninstall(ctx, opts)
}
