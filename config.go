package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Repo             string `toml:"repo"`
	BeadsDir         string `toml:"beads_dir"`
	BranchPrefix     string `toml:"branch_prefix"`
	BaseBranch       string `toml:"base_branch"`
	WorkerAgent      string `toml:"worker_agent"`
	CoordinatorAgent string `toml:"coordinator_agent"`
	CoordinatorName  string `toml:"coordinator_name"`
	TickSeconds      int    `toml:"tick_seconds"`
	GHSeconds        int    `toml:"gh_seconds"`
	// A pane must have been idle this long before the ticker types into it:
	// on Herdr 0.9.1 a prompt merges with whatever the user has half-typed.
	IdleSeconds int  `toml:"idle_seconds"`
	Nudge       bool `toml:"nudge"`
	// Remove a bead's worktree once its PR merged, the bead is closed and its
	// agent is idle. Off by default: removal closes the workspace.
	AutoResolve bool `toml:"auto_resolve"`
	// Only keys with these team prefixes count as Linear issues; empty accepts
	// any key outside a small denylist.
	LinearPrefixes []string `toml:"linear_prefixes"`
	// HerdrSocket is the herdr server the coordinator runs on. The ticker
	// always talks to it, whichever server's plugin startup launched it.
	HerdrSocket string `toml:"herdr_socket"`

	// Repos are named repositories a bead selects with a repo:<name> label;
	// Repo is the default.
	Repos map[string]string `toml:"repos"`
}

func defaultConfig() Config {
	return Config{
		BranchPrefix:     "tj/",
		BaseBranch:       "main",
		WorkerAgent:      "claude",
		CoordinatorAgent: "claude",
		CoordinatorName:  "kelpie",
		TickSeconds:      15,
		GHSeconds:        60,
		IdleSeconds:      60,
		Nudge:            true,
	}
}

// The tool was called shepherd; its config and state lived under that name.
const legacyName = "shepherd"

func configDir() string {
	if d := envDir("KELPIE_CONFIG_DIR", "SHEPHERD_CONFIG_DIR"); d != "" {
		return d
	}
	d, _ := os.UserConfigDir()
	return pickDir(filepath.Join(d, "kelpie"), filepath.Join(d, legacyName))
}

func stateDir() string {
	if d := envDir("KELPIE_STATE_DIR", "SHEPHERD_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".local", "state")
	return pickDir(filepath.Join(base, "kelpie"), filepath.Join(base, legacyName))
}

func envDir(keys ...string) string {
	for _, k := range keys {
		if d := os.Getenv(k); d != "" {
			return d
		}
	}
	return ""
}

// pickDir is dir, or the legacy dir while only that one exists (a migration
// that could not move it).
func pickDir(dir, legacy string) string {
	if _, err := os.Stat(dir); err != nil {
		if fi, err := os.Stat(legacy); err == nil && fi.IsDir() {
			return legacy
		}
	}
	return dir
}

// migrateDirs moves shepherd's config and state to kelpie's paths. Directories
// set through the environment are left where they are.
func migrateDirs() []string {
	var out []string
	if envDir("KELPIE_CONFIG_DIR", "SHEPHERD_CONFIG_DIR") == "" {
		d, _ := os.UserConfigDir()
		out = append(out, migrateDir(filepath.Join(d, legacyName), filepath.Join(d, "kelpie"))...)
	}
	if envDir("KELPIE_STATE_DIR", "SHEPHERD_STATE_DIR") == "" {
		home, _ := os.UserHomeDir()
		base := filepath.Join(home, ".local", "state")
		out = append(out, migrateDir(filepath.Join(base, legacyName), filepath.Join(base, "kelpie"))...)
	}
	return out
}

// migrateDir renames legacy to dir and leaves a symlink at legacy, so a
// process still using the old path (a running older ticker, an open
// coordinator) keeps reading and writing the same files. A rename keeps every
// file, open ones included; it is idempotent, and does nothing once dir exists
// or when legacy is missing or already a link.
func migrateDir(legacy, dir string) []string {
	fi, err := os.Lstat(legacy)
	if err != nil || !fi.IsDir() {
		return nil
	}
	if _, err := os.Lstat(dir); err == nil {
		return []string{fmt.Sprintf("both %s and %s exist; using %s, left %s alone", dir, legacy, dir, legacy)}
	}
	if err := os.Rename(legacy, dir); err != nil {
		return []string{fmt.Sprintf("could not move %s to %s (%v); still using %s", legacy, dir, err, legacy)}
	}
	if err := os.Symlink(dir, legacy); err != nil {
		return []string{fmt.Sprintf("moved %s to %s; no link left behind: %v", legacy, dir, err)}
	}
	return []string{fmt.Sprintf("moved %s to %s (linked from the old path)", legacy, dir)}
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	_, err := toml.DecodeFile(filepath.Join(configDir(), "config.toml"), &cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, err
	}
	cfg.Repo, cfg.BeadsDir = expandHome(cfg.Repo), expandHome(cfg.BeadsDir)
	cfg.HerdrSocket = expandHome(cfg.HerdrSocket)
	for name, path := range cfg.Repos {
		cfg.Repos[name] = expandHome(path)
	}
	if cfg.Repo == "" {
		return cfg, errNoRepo
	}
	if cfg.BeadsDir == "" {
		cfg.BeadsDir = filepath.Join(cfg.Repo, ".beads")
	}
	// Keybindings and plugin actions run in the herdr server's environment,
	// which has no BEADS_DIR; without this bd silently opens another database.
	if os.Getenv("BEADS_DIR") == "" {
		os.Setenv("BEADS_DIR", cfg.BeadsDir)
	}
	return cfg, nil
}

var errNoRepo = errors.New("set repo in " + filepath.Join(configDir(), "config.toml") + ", or run `kelpie configure --repo <path>`")

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}

const repoLabel = "repo:"

// repoName is the name a bead's repo: label selects, "" for the default repo,
// and whether that name is configured.
func (c Config) repoName(b Bead) (string, bool) {
	for _, l := range b.Labels {
		if name, ok := strings.CutPrefix(l, repoLabel); ok {
			_, known := c.Repos[name]
			return name, known
		}
	}
	return "", true
}

// repoFor is the repository a bead's worktree, branch and PRs live in. Every
// per-bead repo lookup goes through here so multi-repo support has one seam.
func (c Config) repoFor(b Bead) string {
	if name, ok := c.repoName(b); ok && name != "" {
		return c.Repos[name]
	}
	return c.Repo
}

// allRepos is every repository kelpie follows, for passes that are not about
// one bead (PR listing, worktree sweeps).
func (c Config) allRepos() []string {
	repos := []string{filepath.Clean(c.Repo)}
	for _, name := range slices.Sorted(maps.Keys(c.Repos)) {
		if p := filepath.Clean(c.Repos[name]); !slices.Contains(repos, p) {
			repos = append(repos, p)
		}
	}
	return repos
}

// coordSocket is the socket of the herdr server the ticker follows: herdr_socket,
// or herdr's default server. Never the caller's HERDR_SOCKET_PATH: every server
// that loads the plugin runs startup, and a ticker on a server without the
// coordinator sees no agents.
func (c Config) coordSocket() string {
	if c.HerdrSocket != "" {
		return c.HerdrSocket
	}
	return defaultHerdrSocket()
}

func (c Config) tick() time.Duration { return time.Duration(max(c.TickSeconds, 5)) * time.Second }
func (c Config) ghEvery() time.Duration {
	return time.Duration(max(c.GHSeconds, 30)) * time.Second
}
func (c Config) idle() time.Duration { return time.Duration(c.IdleSeconds) * time.Second }

func instructionsPath() string { return filepath.Join(configDir(), "instructions.md") }

func readInstructions() string {
	b, err := os.ReadFile(instructionsPath())
	if err != nil {
		return ""
	}
	return string(b)
}
