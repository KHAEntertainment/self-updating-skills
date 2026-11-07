package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installAgentFixture builds a repo with one ci-owned skill, a home
// directory and a stub launchctl, so the command never touches launchd.
func installAgentFixture(t *testing.T) (repo, home, stub string) {
	t.Helper()
	root := t.TempDir()
	repo = filepath.Join(root, "repo")
	skill := filepath.Join(repo, "skills", "alpha")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "upkeep.data.json"), []byte("{\n  \"schema\": 1,\n  \"owner\": \"ci\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts", "upkeep-local-run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	stub = filepath.Join(root, "launchctl-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + filepath.Join(root, "launchctl.log") + "\n" +
		"case \"$1\" in\n  print) echo \"com.khaentertainment.upkeep = { state = running }\" ;;\nesac\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, home, stub
}

func runInstallAgent(t *testing.T, args ...string) string {
	t.Helper()
	// The command's flags are package-level variables, so they keep the
	// value of the previous run; reset them before every call.
	installUninstall = false
	installLink = false
	installRepo = ""
	installPlistDir = ""
	installHarness = nil
	installHour = 0
	installMinute = 0
	installDryRun = false
	installJSON = false
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"install-agent"}, args...))
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exec install-agent %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestInstallAgentCommandEndToEnd(t *testing.T) {
	repo, home, stub := installAgentFixture(t)
	plistDir := filepath.Join(t.TempDir(), "LaunchAgents")
	t.Setenv("HOME", home)
	t.Setenv("UPKEEP_LAUNCHCTL", stub)

	out := runInstallAgent(t, "--repo", repo, "--plist-dir", plistDir, "--link")
	if !strings.Contains(out, "installed launchd agent com.khaentertainment.upkeep") {
		t.Errorf("install output = %q", out)
	}
	plist := filepath.Join(plistDir, "com.khaentertainment.upkeep.plist")
	b, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("the plist was not written: %v", err)
	}
	if strings.Contains(string(b), "ANTHROPIC") || strings.Contains(string(b), "API_KEY") {
		t.Errorf("the plist holds a secret:\n%s", b)
	}
	// The ci-owned skill resolves from ~/.agents/skills into the repo.
	target, err := os.Readlink(filepath.Join(home, ".agents", "skills", "alpha"))
	if err != nil {
		t.Fatalf("the skill was not linked: %v", err)
	}
	if want := filepath.Join(repo, "skills", "alpha"); target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}
	// The kick command is printed so the agent can be started by hand.
	if !strings.Contains(out, "kickstart") {
		t.Errorf("the install output does not name the kick command:\n%s", out)
	}

	out = runInstallAgent(t, "--repo", repo, "--plist-dir", plistDir, "--uninstall")
	if !strings.Contains(out, "removed launchd agent com.khaentertainment.upkeep") {
		t.Errorf("uninstall output = %q", out)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Errorf("the plist was not removed")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("the link was not removed")
	}
}

func TestInstallAgentWithoutLinkTouchesNothing(t *testing.T) {
	repo, home, stub := installAgentFixture(t)
	plistDir := filepath.Join(t.TempDir(), "LaunchAgents")
	t.Setenv("HOME", home)
	t.Setenv("UPKEEP_LAUNCHCTL", stub)

	out := runInstallAgent(t, "--repo", repo, "--plist-dir", plistDir)
	if !strings.Contains(out, "--link") {
		t.Errorf("the output does not mention the explicit link flag:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("a link was created without --link")
	}
}
