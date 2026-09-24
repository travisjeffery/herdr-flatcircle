package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
}

func defaultConfig() Config {
	return Config{
		BranchPrefix:     "tj/",
		BaseBranch:       "main",
		WorkerAgent:      "claude",
		CoordinatorAgent: "claude",
		CoordinatorName:  "shepherd",
		TickSeconds:      15,
		GHSeconds:        60,
		IdleSeconds:      60,
		Nudge:            true,
	}
}

func configDir() string {
	if d := os.Getenv("SHEPHERD_CONFIG_DIR"); d != "" {
		return d
	}
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "shepherd")
}

func stateDir() string {
	if d := os.Getenv("SHEPHERD_STATE_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "shepherd")
}

func loadConfig() (Config, error) {
	cfg := defaultConfig()
	_, err := toml.DecodeFile(filepath.Join(configDir(), "config.toml"), &cfg)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return cfg, err
	}
	cfg.Repo, cfg.BeadsDir = expandHome(cfg.Repo), expandHome(cfg.BeadsDir)
	if cfg.Repo == "" {
		return cfg, errors.New("set repo in " + filepath.Join(configDir(), "config.toml"))
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

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
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
