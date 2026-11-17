package aristotle

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Git runs git outside the sandbox, only in repositories Aristotle owns: no
// system or global config, no hooks, no prompts. The token, when set, goes
// in the environment as an auth header, never in a URL or argument.
type Git struct{ Home, Token string }

const remote = "https://github.com/" + Repo + ".git"

func (g Git) run(ctx context.Context, dir string, args ...string) (string, error) {
	args = append([]string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "HOME=" + g.Home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	if g.Token != "" {
		auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + g.Token))
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraheader", "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic "+auth)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[4], err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// Sync brings the bare mirror up to date with main and returns main's commit.
func (g Git) Sync(ctx context.Context, mirror string) (string, error) {
	if _, err := g.run(ctx, "", "init", "--bare", "--quiet", mirror); err != nil {
		return "", err
	}
	if _, err := g.run(ctx, mirror, "fetch", "--quiet", remote, "+refs/heads/main:refs/heads/main"); err != nil {
		return "", err
	}
	return g.run(ctx, mirror, "rev-parse", "refs/heads/main")
}

// Checkout clones base from the mirror into dir on a new branch, with no
// remote: the agent works offline from GitHub.
func (g Git) Checkout(ctx context.Context, mirror, dir, branch, base string) error {
	if _, err := g.run(ctx, "", "clone", "--quiet", "--no-local", "--no-checkout", mirror, dir); err != nil {
		return err
	}
	if _, err := g.run(ctx, dir, "checkout", "--quiet", "-b", branch, base); err != nil {
		return err
	}
	_, err := g.run(ctx, dir, "remote", "remove", "origin")
	return err
}

// Import fetches the agent's bundle into the mirror as ref and checks it: at
// least one commit on top of base, none a merge, all authored and committed
// as email. It returns the commit count.
func (g Git) Import(ctx context.Context, mirror, bundle, ref, base, email string) (int, error) {
	if _, err := g.run(ctx, mirror, "fetch", "--quiet", bundle, "+HEAD:"+ref); err != nil {
		return 0, err
	}
	if _, err := g.run(ctx, mirror, "merge-base", "--is-ancestor", base, ref); err != nil {
		return 0, errors.New("the agent's branch does not build on main")
	}
	log, err := g.run(ctx, mirror, "log", "--format=%P|%ae|%ce", base+".."+ref)
	if err != nil {
		return 0, err
	}
	if log == "" {
		return 0, errors.New("the agent made no commits")
	}
	lines := strings.Split(log, "\n")
	for _, l := range lines {
		parents, who, _ := strings.Cut(l, "|")
		if strings.Contains(parents, " ") {
			return 0, errors.New("the agent's branch contains a merge commit")
		}
		if who != email+"|"+email {
			return 0, fmt.Errorf("commit not authored as Aristotle: %s", who)
		}
	}
	return len(lines), nil
}

// Push publishes ref as branch. It never forces.
func (g Git) Push(ctx context.Context, mirror, ref, branch string) error {
	_, err := g.run(ctx, mirror, "push", "--quiet", remote, ref+":refs/heads/"+branch)
	return err
}
