package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubLaunchctl writes a fake launchctl that records every call and
// answers "print" with the job label, so the tests never touch launchd.
func stubLaunchctl(t *testing.T, dir string) (bin string, log string) {
	t.Helper()
	bin = filepath.Join(dir, "launchctl-stub")
	log = filepath.Join(dir, "launchctl.log")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + log + "\n" +
		"case \"$1\" in\n" +
		"  print) echo \"com.khaentertainment.upkeep = { state = running }\" ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

// fakeRepo builds a repo with one ci-owned skill and the job script.
func fakeRepo(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
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
	scripts := filepath.Join(repo, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "upkeep-local-run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

// fakeHome builds a home directory with a local-owned skill and a harness
// entry that already exists.
func fakeHome(t *testing.T, root string) string {
	t.Helper()
	home := filepath.Join(root, "home")
	local := filepath.Join(home, ".agents", "skills", "beta")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("---\nname: beta\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "upkeep.data.json"), []byte("{\n  \"schema\": 1,\n  \"owner\": \"local\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func testOptions(t *testing.T, repo, home, root string) Options {
	t.Helper()
	bin, _ := stubLaunchctl(t, root)
	return Options{
		Repo:      repo,
		Home:      home,
		PlistDir:  filepath.Join(root, "LaunchAgents"),
		Launchctl: bin,
		Binary:    filepath.Join(root, "upkeep"),
		UID:       "501",
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallWritesPlistWithoutSecret(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)

	res, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlistState != "written" {
		t.Fatalf("plist state = %q, want written", res.PlistState)
	}
	b, err := os.ReadFile(res.Plist)
	if err != nil {
		t.Fatal(err)
	}
	plist := string(b)
	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<key>StartCalendarInterval</key>",
		"<integer>7</integer>",
		"<integer>17</integer>",
		"upkeep-local-run.sh",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist is missing %q:\n%s", want, plist)
		}
	}
	// The plist must never hold a secret.
	for _, bad := range []string{"ANTHROPIC", "API_KEY", "sk-ant", "secret", "password"} {
		if strings.Contains(plist, bad) {
			t.Errorf("plist mentions %q; it must hold no secret:\n%s", bad, plist)
		}
	}
}

func TestInstallLoadsTheJob(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)
	bin, log := stubLaunchctl(t, root)
	opts.Launchctl = bin

	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, log)
	if !strings.Contains(got, "bootstrap gui/501 "+filepath.Join(root, "LaunchAgents", PlistName)) {
		t.Errorf("launchctl was not asked to bootstrap the plist:\n%s", got)
	}
	if !strings.Contains(got, "bootout gui/501/"+Label) {
		t.Errorf("launchctl was not asked to bootout the old job:\n%s", got)
	}
}

func TestInstallLinksCiOwnedSkillAndHarnesses(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)
	opts.Link = true

	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	// The ci-owned skill now resolves from ~/.agents/skills into the repo.
	link := filepath.Join(home, ".agents", "skills", "alpha")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("~/.agents/skills/alpha is not a symlink: %v", err)
	}
	want := filepath.Join(repo, "skills", "alpha")
	if target != want {
		t.Errorf("symlink target = %q, want %q", target, want)
	}

	// Every harness dir gets a relative link, in the style of the
	// existing entries.
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{filepath.Join(home, ".claude", "skills"), "../../.agents/skills/alpha"},
		{filepath.Join(home, ".codex", "skills"), "../../.agents/skills/alpha"},
		{filepath.Join(home, ".config", "opencode", "skills"), "../../../.agents/skills/alpha"},
		{filepath.Join(home, ".gemini", "config", "skills"), "../../../.agents/skills/alpha"},
		{filepath.Join(home, ".cursor", "skills"), "../../.agents/skills/alpha"},
	} {
		if err := os.MkdirAll(tc.dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Re-run so the harness dirs that were created above are picked up.
	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{filepath.Join(home, ".claude", "skills"), "../../.agents/skills/alpha"},
		{filepath.Join(home, ".codex", "skills"), "../../.agents/skills/alpha"},
		{filepath.Join(home, ".config", "opencode", "skills"), "../../../.agents/skills/alpha"},
		{filepath.Join(home, ".gemini", "config", "skills"), "../../../.agents/skills/alpha"},
		{filepath.Join(home, ".cursor", "skills"), "../../.agents/skills/alpha"},
	} {
		got, err := os.Readlink(filepath.Join(tc.dir, "alpha"))
		if err != nil {
			t.Errorf("%s/alpha is not a symlink: %v", tc.dir, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s/alpha -> %q, want %q", tc.dir, got, tc.want)
		}
	}
	// The local-owned skill is linked into the harness dirs too.
	if got, err := os.Readlink(filepath.Join(home, ".gemini", "config", "skills", "beta")); err != nil {
		t.Errorf("the local-owned skill was not linked into the harness dir: %v", err)
	} else if want := "../../../.agents/skills/beta"; got != want {
		t.Errorf("beta -> %q, want %q", got, want)
	}
}

func TestInstallLeavesExistingHarnessSymlinkAlone(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(claude, "alpha")
	if err := os.Symlink("/somewhere/else", existing); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(t, repo, home, root)
	opts.Link = true

	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(existing)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/somewhere/else" {
		t.Errorf("the existing harness symlink was changed to %q; it must stay", got)
	}
}

func TestInstallWithoutLinkTouchesNoLink(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)

	res, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SkillLinks) != 0 || len(res.HarnessLinks) != 0 {
		t.Errorf("links were touched without --link: %+v %+v", res.SkillLinks, res.HarnessLinks)
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("~/.agents/skills/alpha was created without --link")
	}
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)
	opts.Link = true
	opts.DryRun = true

	res, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlistState != "written" {
		t.Errorf("plist state = %q, want written", res.PlistState)
	}
	if _, err := os.Stat(res.Plist); !os.IsNotExist(err) {
		t.Errorf("the plist was written in dry-run mode")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("a link was created in dry-run mode")
	}
}

func TestUninstallRemovesOnlyCreatedLinks(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	claude := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	preExisting := filepath.Join(claude, "alpha")
	if err := os.Symlink("/somewhere/else", preExisting); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(t, repo, home, root)
	opts.Link = true

	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	res, err := Uninstall(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlistState != "removed" {
		t.Errorf("plist state = %q, want removed", res.PlistState)
	}
	if _, err := os.Stat(res.Plist); !os.IsNotExist(err) {
		t.Errorf("the plist was not removed")
	}
	if _, err := os.Lstat(preExisting); err != nil {
		t.Errorf("the pre-existing harness symlink was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".gemini", "config", "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("a created harness link was not removed")
	}
	if _, err := os.Lstat(filepath.Join(home, ".agents", "skills", "alpha")); !os.IsNotExist(err) {
		t.Errorf("the created skill link was not removed")
	}
}

func TestUninstallRestoresAMovedAsideDirectory(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	// A real directory sits where the symlink has to go.
	agentsSkills := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(filepath.Join(agentsSkills, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(agentsSkills, "alpha", "SKILL.md")
	if err := os.WriteFile(marker, []byte("---\nname: alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(t, repo, home, root)
	opts.Link = true

	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(agentsSkills, "alpha")); err != nil {
		t.Fatalf("the directory was not replaced by a symlink: %v", err)
	}
	if fi, err := os.Lstat(filepath.Join(agentsSkills, "alpha")); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the entry is not a symlink")
	}
	if _, err := Uninstall(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the moved-aside directory was not restored: %v", err)
	}
	if !strings.Contains(string(b), "name: alpha") {
		t.Errorf("the restored directory has the wrong content: %q", b)
	}
}

func TestUninstallWithoutAManifestIsSafe(t *testing.T) {
	root := t.TempDir()
	repo := fakeRepo(t, root)
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)

	res, err := Uninstall(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.PlistState != "absent" {
		t.Errorf("plist state = %q, want absent", res.PlistState)
	}
}

func TestInstallRefusesWithoutTheJobScript(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	home := fakeHome(t, root)
	opts := testOptions(t, repo, home, root)
	if _, err := Install(context.Background(), opts); err == nil {
		t.Fatal("install succeeded without the job script")
	}
}
