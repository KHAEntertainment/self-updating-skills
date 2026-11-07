// Package runner implements the local half of the Skill Engine's runners
// (architecture D9, decision P3): the launchd agent that keeps local-owned
// skills current and pulls the repo for the ci-owned ones.
//
// Nothing in this package holds a secret. The plist names the job script
// and the binary; the API key is read from the user's keychain through
// Enigma at run time, by the script, and never reaches a file, the plist
// or a log line.
//
// Every machine-specific path is derived from Options, so the whole
// install/uninstall flow is testable against a temporary HOME, a temporary
// plist directory and a stub launchctl.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/KHAEntertainment/self-updating-skills/internal/render"
)

const (
	// Label is the launchd job label of the local runner.
	Label = "com.khaentertainment.upkeep"
	// PlistName is the plist file name inside the LaunchAgents directory.
	PlistName = Label + ".plist"
	// JobScript is the repo-relative path of the script the job runs.
	JobScript = "scripts/upkeep-local-run.sh"
	// JobHour and JobMinute are the daily StartCalendarInterval time.
	// 07:17 is off the hour and off the half hour, which launchd's own
	// documentation asks for (many jobs share :00 and :30).
	JobHour   = 7
	JobMinute = 17

	// manifestName is the install manifest inside the upkeep state dir.
	manifestName = "agent-install.json"
	// backupDirName is the directory (inside the upkeep state dir) where a
	// replaced skill directory is kept, so uninstall can restore it. It
	// sits outside ~/.agents/skills so no harness ever lists a backup as
	// a skill.
	backupDirName = "backup"
)

// Options configures an install or an uninstall. The zero value is
// usable: every field has a default derived from the environment.
type Options struct {
	// Repo is the repo root that holds skills/ and the job script.
	Repo string `json:"repo"`
	// Home is the user's home directory.
	Home string `json:"home"`
	// PlistDir is the directory the plist is written to.
	PlistDir string `json:"plist_dir"`
	// Launchctl is the launchctl binary to drive.
	Launchctl string `json:"launchctl"`
	// Binary is the upkeep binary the job runs.
	Binary string `json:"binary"`
	// UID is the launchd domain uid (the user's numeric id).
	UID string `json:"uid"`
	// Link is the explicit switch: ci-owned entries under
	// ~/.agents/skills become symlinks into the repo, and every owned
	// skill is linked into every harness skills dir.
	Link bool `json:"link"`
	// HarnessDirs are extra harness skills dirs, added to the defaults.
	HarnessDirs []string `json:"harness_dirs"`
	// Hour and Minute are the daily run time.
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
	// DryRun computes the plan and writes nothing.
	DryRun bool `json:"dry_run"`
}

// LinkChange records one symlink the install created, repointed or left
// alone.
type LinkChange struct {
	Path   string `json:"path"`
	Target string `json:"target"`
	Action string `json:"action"` // created | repointed | unchanged | skipped | removed | restored
	Note   string `json:"note,omitempty"`
}

// Result is the outcome of an install or an uninstall.
type Result struct {
	Action       string       `json:"action"`
	Label        string       `json:"label"`
	Plist        string       `json:"plist"`
	PlistState   string       `json:"plist_state"` // written | unchanged | removed | absent
	Loaded       bool         `json:"loaded"`
	KickCommand  string       `json:"kick_command"`
	SkillLinks   []LinkChange `json:"skill_links"`
	HarnessLinks []LinkChange `json:"harness_links"`
	Removed      []string     `json:"removed"`
	Notes        []string     `json:"notes"`
}

// skillRef is one owned skill: a name and the directory it lives in.
type skillRef struct {
	Name string
	Dir  string
}

// withDefaults fills the unset fields and validates the rest.
func (o Options) withDefaults() (Options, error) {
	var err error
	if o.Repo == "" {
		o.Repo, err = os.Getwd()
		if err != nil {
			return o, err
		}
	}
	if o.Repo, err = filepath.Abs(o.Repo); err != nil {
		return o, err
	}
	if o.Home == "" {
		o.Home, err = os.UserHomeDir()
		if err != nil {
			return o, err
		}
	}
	if o.PlistDir == "" {
		o.PlistDir = os.Getenv("UPKEEP_LAUNCHD_DIR")
	}
	if o.PlistDir == "" {
		o.PlistDir = filepath.Join(o.Home, "Library", "LaunchAgents")
	}
	if o.Launchctl == "" {
		o.Launchctl = os.Getenv("UPKEEP_LAUNCHCTL")
	}
	if o.Launchctl == "" {
		o.Launchctl = "launchctl"
	}
	if o.Binary == "" {
		o.Binary, err = os.Executable()
		if err != nil {
			return o, err
		}
	}
	if o.UID == "" {
		o.UID = strconv.Itoa(os.Getuid())
	}
	if o.Hour == 0 && o.Minute == 0 {
		o.Hour, o.Minute = JobHour, JobMinute
	}
	if o.Hour < 0 || o.Hour > 23 || o.Minute < 0 || o.Minute > 59 {
		return o, fmt.Errorf("runner: %02d:%02d is not a valid daily run time", o.Hour, o.Minute)
	}
	return o, nil
}

// stateDir is the upkeep state directory under the user's home.
func (o Options) stateDir() string { return filepath.Join(o.Home, ".agents", "upkeep") }

// manifestPath is the install manifest's path.
func (o Options) manifestPath() string { return filepath.Join(o.stateDir(), manifestName) }

// agentsSkillsDir is the shared skills directory under the user's home.
func (o Options) agentsSkillsDir() string { return filepath.Join(o.Home, ".agents", "skills") }

// launchdTarget is the launchd service target of the job.
func (o Options) launchdTarget() string { return "gui/" + o.UID + "/" + Label }

// kickCommand is the command that starts the job once, right now.
func (o Options) kickCommand() string {
	return o.Launchctl + " kickstart -k " + o.launchdTarget()
}

// defaultHarnessDirs lists the harness skills dirs this repo links into.
// Antigravity's ~/.gemini/config/skills is the one that matters: it reads
// user skills from there and from nowhere else (Phase 2 harness checks,
// issue #1 comment 6458680218).
func defaultHarnessDirs(home string) []string {
	return []string{
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".config", "opencode", "skills"),
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(home, ".gemini", "skills"),
		filepath.Join(home, ".cursor", "skills"),
		filepath.Join(home, ".openclaw", "skills"),
	}
}

// harnessDirs returns the harness dirs to link into: the defaults plus
// any extra ones the caller named.
func (o Options) harnessDirs() []string {
	dirs := defaultHarnessDirs(o.Home)
	dirs = append(dirs, o.HarnessDirs...)
	return dirs
}

// scanOwned lists the skills under root whose sidecar names the given
// owner. A missing sidecar is not an error (the skill has no owner); a
// malformed one is reported as a note and the skill is skipped.
func scanOwned(root, owner string) ([]skillRef, []string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var out []skillRef
	var notes []string
	for _, e := range entries {
		// A skill directory, or a symlink to one.
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		dir := filepath.Join(root, e.Name())
		sc, err := render.LoadSidecar(dir)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v (skipped)", dir, err))
			continue
		}
		if sc == nil || sc.Owner != owner {
			continue
		}
		out = append(out, skillRef{Name: e.Name(), Dir: dir})
	}
	return out, notes, nil
}

// ownedSkills finds the ci-owned skills in the repo and the local-owned
// skills under ~/.agents/skills.
func ownedSkills(o Options) (ci, local []skillRef, notes []string, err error) {
	ci, n, err := scanOwned(filepath.Join(o.Repo, "skills"), "ci")
	if err != nil {
		return nil, nil, nil, err
	}
	notes = append(notes, n...)
	local, n, err = scanOwned(o.agentsSkillsDir(), "local")
	if err != nil {
		return nil, nil, nil, err
	}
	notes = append(notes, n...)
	return ci, local, notes, nil
}

// plistContent renders the launchd plist. It holds no secret: the only
// values are paths, the label and the daily run time.
func plistContent(o Options, script string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	b.WriteString(`<plist version="1.0">` + "\n")
	b.WriteString("<dict>\n")
	plistKey(&b, "Label")
	plistStringValue(&b, Label)
	plistKey(&b, "ProgramArguments")
	b.WriteString("<array>\n")
	plistStringValue(&b, "/bin/bash")
	plistStringValue(&b, script)
	plistStringValue(&b, o.Repo)
	plistStringValue(&b, o.Binary)
	b.WriteString("</array>\n")
	plistKey(&b, "StartCalendarInterval")
	b.WriteString("<dict>\n")
	plistKey(&b, "Hour")
	fmt.Fprintf(&b, "<integer>%d</integer>\n", o.Hour)
	plistKey(&b, "Minute")
	fmt.Fprintf(&b, "<integer>%d</integer>\n", o.Minute)
	b.WriteString("</dict>\n")
	plistKey(&b, "RunAtLoad")
	b.WriteString("<false/>\n")
	plistKey(&b, "WorkingDirectory")
	plistStringValue(&b, o.Repo)
	plistKey(&b, "StandardOutPath")
	plistStringValue(&b, filepath.Join(o.stateDir(), "agent.out.log"))
	plistKey(&b, "StandardErrorPath")
	plistStringValue(&b, filepath.Join(o.stateDir(), "agent.err.log"))
	plistKey(&b, "EnvironmentVariables")
	b.WriteString("<dict>\n")
	plistKey(&b, "HOME")
	plistStringValue(&b, o.Home)
	plistKey(&b, "PATH")
	plistStringValue(&b, "/usr/bin:/bin:/usr/sbin:/sbin")
	b.WriteString("</dict>\n")
	b.WriteString("</dict>\n")
	b.WriteString("</plist>\n")
	return b.String()
}

func plistKey(b *strings.Builder, key string) {
	fmt.Fprintf(b, "\t<key>%s</key>\n", xmlEscape(key))
}

func plistStringValue(b *strings.Builder, val string) {
	fmt.Fprintf(b, "\t<string>%s</string>\n", xmlEscape(val))
}

// xmlEscape escapes a value for an XML text node.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	// xml.EscapeText only fails on a writer error, and a bytes.Buffer
	// never fails.
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// manifest is the record of what the install created, so an uninstall
// removes exactly those links and nothing else.
type manifest struct {
	Version int            `json:"version"`
	Label   string         `json:"label"`
	Plist   string         `json:"plist"`
	Created []manifestLink `json:"created_links"`
	Backups []manifestLink `json:"backups"`
}

// manifestLink is one path and the target it was created with.
type manifestLink struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func loadManifest(path string) (*manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

func saveManifest(path string, m *manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Install writes the plist, loads the job and (with the explicit Link
// flag) switches the ci-owned skills to repo symlinks and links every
// owned skill into every harness skills dir.
func Install(ctx context.Context, opts Options) (*Result, error) {
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	res := &Result{
		Action: "install",
		Label:  Label,
		Notes: []string{
			"The plist holds no secret: register the API key once with " +
				"`enigma add ANTHROPIC_API_KEY --scope global --depository keychain --usage unattended`, " +
				"and the job script reads it from the keychain at run time.",
		},
	}

	script := filepath.Join(o.Repo, JobScript)
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("runner: job script %s is missing: %w", script, err)
	}

	ciOwned, localOwned, notes, err := ownedSkills(o)
	if err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes, notes...)

	// The plist.
	plistPath := filepath.Join(o.PlistDir, PlistName)
	res.Plist = plistPath
	content := plistContent(o, script)
	existing, err := os.ReadFile(plistPath)
	switch {
	case err != nil && !os.IsNotExist(err):
		return nil, err
	case err == nil && string(existing) == content:
		res.PlistState = "unchanged"
	default:
		res.PlistState = "written"
		if !o.DryRun {
			if err := os.MkdirAll(o.PlistDir, 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
				return nil, err
			}
		}
	}

	// The job: unload whatever is loaded, then bootstrap the new plist.
	if !o.DryRun {
		// A bootout of a job that was never loaded fails; that is the
		// normal first-install case, so the error is ignored.
		_, _ = o.launchctl(ctx, "bootout", o.launchdTarget())
		if out, err := o.launchctl(ctx, "bootstrap", "gui/"+o.UID, plistPath); err != nil {
			return nil, fmt.Errorf("runner: launchctl bootstrap failed: %w (%s)", err, strings.TrimSpace(out))
		}
	}
	res.Loaded = o.jobLoaded(ctx)
	res.KickCommand = o.kickCommand()

	// The links (only with the explicit flag).
	var m manifest
	m.Version = 1
	m.Label = Label
	m.Plist = plistPath
	if o.Link {
		if err := o.linkSkills(res, &m, ciOwned); err != nil {
			return nil, err
		}
		if err := o.linkHarness(res, &m, ciOwned, localOwned); err != nil {
			return nil, err
		}
	} else {
		res.Notes = append(res.Notes,
			"Links were not touched: pass --link to switch the ci-owned skills to repo symlinks "+
				"and to link every owned skill into every harness skills dir.")
	}

	if !o.DryRun {
		if err := saveManifest(o.manifestPath(), &m); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// linkSkills switches the ci-owned entries under ~/.agents/skills into
// symlinks into the repo. An existing directory is moved aside (and
// recorded) so an uninstall can put it back; the harness symlinks that
// point at it are left alone and keep resolving.
func (o Options) linkSkills(res *Result, m *manifest, ciOwned []skillRef) error {
	for _, s := range ciOwned {
		target := filepath.Join(o.Repo, "skills", s.Name)
		link := filepath.Join(o.agentsSkillsDir(), s.Name)
		ch, err := o.ensureSymlink(link, target, switchMode, false)
		if err != nil {
			return err
		}
		res.SkillLinks = append(res.SkillLinks, ch)
		switch ch.Action {
		case "created", "repointed":
			m.Created = append(m.Created, manifestLink{Path: link, Target: target})
		case "moved":
			// An existing directory was moved aside: record where it went
			// so an uninstall can restore it.
			backup := filepath.Join(o.stateDir(), backupDirName, s.Name)
			m.Backups = append(m.Backups, manifestLink{Path: link, Target: backup})
		}
	}
	return nil
}

// linkHarness links every owned skill into every harness skills dir. A
// harness entry that already exists is never touched: the existing
// symlinks point at ~/.agents/skills and keep resolving through the new
// chain.
func (o Options) linkHarness(res *Result, m *manifest, ciOwned, localOwned []skillRef) error {
	owned := append(append([]skillRef(nil), ciOwned...), localOwned...)
	for _, dir := range o.harnessDirs() {
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				res.Notes = append(res.Notes, fmt.Sprintf("harness dir %s does not exist (skipped)", dir))
				continue
			}
			return err
		}
		for _, s := range owned {
			target := filepath.Join(o.agentsSkillsDir(), s.Name)
			link := filepath.Join(dir, s.Name)
			ch, err := o.ensureSymlink(link, target, createOnlyMode, true)
			if err != nil {
				return err
			}
			res.HarnessLinks = append(res.HarnessLinks, ch)
			if ch.Action == "created" || ch.Action == "repointed" {
				m.Created = append(m.Created, manifestLink{Path: link, Target: target})
			}
		}
	}
	return nil
}

// linkMode says what to do with an entry that already exists.
type linkMode int

const (
	// switchMode creates the link, or repoints an existing symlink that
	// points somewhere else. It is used for the ~/.agents/skills entry,
	// whose whole point is to become a link into the repo.
	switchMode linkMode = iota
	// createOnlyMode leaves an existing entry alone. It is used for the
	// harness dirs: the harness symlinks stay unchanged, so they keep
	// resolving through the chain.
	createOnlyMode
)

// ensureSymlink makes path a symlink to target.
//
// A relative target is relative to the link's own directory, which is the
// style the existing harness entries use ("../../.agents/skills/<name>").
// The link into the repo is absolute, which is the style of the
// ~/.agents/upkeep/bin/upkeep.sh precedent.
func (o Options) ensureSymlink(path, target string, mode linkMode, relative bool) (LinkChange, error) {
	ch := LinkChange{Path: path, Target: target}
	link := target
	if relative {
		rel, err := filepath.Rel(filepath.Dir(path), target)
		if err != nil {
			return ch, err
		}
		link = rel
	}
	fi, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		if o.DryRun {
			ch.Action = "created"
			return ch, nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return ch, err
		}
		if err := os.Symlink(link, path); err != nil {
			return ch, err
		}
		ch.Action = "created"
		return ch, nil
	case err != nil:
		return ch, err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		if mode == createOnlyMode {
			ch.Action = "unchanged"
			ch.Note = "an entry already exists there (left alone)"
			return ch, nil
		}
		// A real directory: move it aside so the link can take its place
		// and an uninstall can restore it.
		backup := filepath.Join(o.stateDir(), backupDirName, filepath.Base(path))
		if _, err := os.Stat(backup); err == nil {
			ch.Action = "skipped"
			ch.Note = fmt.Sprintf("a backup already exists at %s (left alone)", backup)
			return ch, nil
		}
		if o.DryRun {
			ch.Action = "moved"
			ch.Note = fmt.Sprintf("the existing directory would be moved to %s", backup)
			return ch, nil
		}
		if err := os.MkdirAll(filepath.Dir(backup), 0o755); err != nil {
			return ch, err
		}
		if err := os.Rename(path, backup); err != nil {
			return ch, err
		}
		if err := os.Symlink(link, path); err != nil {
			// Put the directory back: a half-done switch is worse than
			// no switch at all.
			_ = os.Rename(backup, path)
			return ch, err
		}
		ch.Action = "moved"
		ch.Note = fmt.Sprintf("the existing directory was moved to %s", backup)
		return ch, nil
	}
	// An existing symlink.
	cur, err := os.Readlink(path)
	if err != nil {
		return ch, err
	}
	if cur == link || cur == target {
		ch.Action = "unchanged"
		return ch, nil
	}
	if mode == createOnlyMode {
		ch.Action = "unchanged"
		ch.Note = fmt.Sprintf("it points at %s (left alone)", cur)
		return ch, nil
	}
	if o.DryRun {
		ch.Action = "repointed"
		ch.Note = fmt.Sprintf("was %s", cur)
		return ch, nil
	}
	if err := os.Remove(path); err != nil {
		return ch, err
	}
	if err := os.Symlink(link, path); err != nil {
		return ch, err
	}
	ch.Action = "repointed"
	ch.Note = fmt.Sprintf("was %s", cur)
	return ch, nil
}

// Uninstall unloads the job, removes the plist and removes exactly the
// links the install created.
func Uninstall(ctx context.Context, opts Options) (*Result, error) {
	o, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	res := &Result{Action: "uninstall", Label: Label, KickCommand: o.kickCommand()}

	// Unload the job. A job that was never loaded is not an error.
	if !o.DryRun {
		_, _ = o.launchctl(ctx, "bootout", o.launchdTarget())
	}
	res.Loaded = o.jobLoaded(ctx)

	m, err := loadManifest(o.manifestPath())
	if err != nil {
		return nil, err
	}
	res.Plist = filepath.Join(o.PlistDir, PlistName)
	if m != nil {
		res.Plist = m.Plist
	}

	// Remove the links the install created, and only those: a link that
	// was repointed since the install is left alone.
	if m != nil {
		for _, l := range m.Created {
			cur, err := os.Readlink(l.Path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			want := l.Target
			if rel, err := filepath.Rel(filepath.Dir(l.Path), l.Target); err == nil {
				want = rel
			}
			if cur != want && cur != l.Target {
				res.Notes = append(res.Notes,
					fmt.Sprintf("%s now points at %s (left alone)", l.Path, cur))
				continue
			}
			if !o.DryRun {
				if err := os.Remove(l.Path); err != nil {
					return nil, err
				}
			}
			res.Removed = append(res.Removed, l.Path)
		}
		// Put back a directory the install moved aside.
		for _, b := range m.Backups {
			if _, err := os.Stat(b.Path); err == nil {
				res.Notes = append(res.Notes,
					fmt.Sprintf("%s exists again (the backup at %s was left alone)", b.Path, b.Target))
				continue
			}
			if _, err := os.Stat(b.Target); err != nil {
				continue
			}
			if !o.DryRun {
				if err := os.MkdirAll(filepath.Dir(b.Path), 0o755); err != nil {
					return nil, err
				}
				if err := os.Rename(b.Target, b.Path); err != nil {
					return nil, err
				}
			}
			res.Removed = append(res.Removed, b.Target)
		}
	}

	// Remove the plist.
	if _, err := os.Stat(res.Plist); err == nil {
		if !o.DryRun {
			if err := os.Remove(res.Plist); err != nil {
				return nil, err
			}
		}
		res.PlistState = "removed"
		res.Removed = append(res.Removed, res.Plist)
	} else {
		res.PlistState = "absent"
	}

	if !o.DryRun {
		if err := os.Remove(o.manifestPath()); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return res, nil
}

// launchctl runs the launchctl binary and returns its combined output.
func (o Options) launchctl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, o.Launchctl, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// jobLoaded reports whether launchd knows the job.
func (o Options) jobLoaded(ctx context.Context) bool {
	out, err := o.launchctl(ctx, "print", o.launchdTarget())
	return err == nil && strings.Contains(out, Label)
}
