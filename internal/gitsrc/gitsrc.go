// Package gitsrc fetches a source repository so it can be audited like any
// other package.
//
// A repository is untrusted input, so the clone is deliberately narrow: HTTPS
// only, a shallow single-branch checkout, no hooks, no submodules, no
// credential prompts, none of the user's git configuration, and a ceiling on
// time and size. Nothing from the clone is ever executed. The caller copies
// the checked-out files into the audit's sandbox, which skips the .git folder,
// symlinks and large files.
package gitsrc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Limits of a clone.
const (
	// CloneTimeout bounds the whole clone.
	CloneTimeout = 3 * time.Minute
	// MaxCloneBytes is the most a clone may put on disk before it is stopped.
	MaxCloneBytes int64 = 768 << 20
)

// Repo is a repository named by a link.
type Repo struct {
	// CloneURL is the HTTPS URL passed to git.
	CloneURL string
	// Owner and Name come from the path: github.com/<owner>/<name>.
	Owner, Name string
	// Ref is the branch or tag the link points at, or "" for the default.
	Ref string
}

// Display is the short name shown to the user: owner/name.
func (r Repo) Display() string {
	if r.Owner != "" {
		return r.Owner + "/" + r.Name
	}
	return r.Name
}

// ErrNotARepo is returned for a link that is not a cloneable repository.
var ErrNotARepo = errors.New("that is not a repository link")

var scpLike = regexp.MustCompile(`^git@([A-Za-z0-9.-]+):(.+)$`)

// ParseRepo turns a pasted link into a repository.
//
// It accepts https links (with or without .git, and with a /tree/<ref> suffix
// as GitHub, GitLab and Codeberg write them) and the scp-like git@host:owner/repo
// form, which is rewritten to HTTPS. Anything else, including http, file, ssh
// and git URLs, hosts that are IP addresses or have no dot, is refused.
func ParseRepo(raw string) (Repo, error) {
	raw = strings.TrimSpace(raw)
	if m := scpLike.FindStringSubmatch(raw); m != nil {
		raw = "https://" + m[1] + "/" + m[2]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return Repo{}, fmt.Errorf("%w: only https links are accepted", ErrNotARepo)
	}
	host := strings.ToLower(u.Hostname())
	if u.Port() != "" && u.Port() != "443" {
		return Repo{}, fmt.Errorf("%w: unusual ports are not accepted", ErrNotARepo)
	}
	if !strings.Contains(host, ".") || host == "localhost" || strings.HasSuffix(host, ".local") || net.ParseIP(host) != nil {
		return Repo{}, fmt.Errorf("%w: the host %q is not a public hostname", ErrNotARepo, host)
	}

	if strings.Contains(u.EscapedPath(), "%") {
		return Repo{}, fmt.Errorf("%w: the path has unexpected characters", ErrNotARepo)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Drop GitLab's "/-/" marker.
	clean := parts[:0]
	for _, p := range parts {
		if p != "" && p != "-" {
			clean = append(clean, p)
		}
	}
	parts = clean
	if len(parts) < 2 {
		return Repo{}, fmt.Errorf("%w: expected host/owner/repository", ErrNotARepo)
	}

	repo := Repo{Owner: parts[0], Name: strings.TrimSuffix(parts[1], ".git")}
	rest := parts[2:]
	if len(rest) >= 2 && (rest[0] == "tree" || rest[0] == "blob" || rest[0] == "releases") {
		if rest[0] == "releases" {
			// .../releases/tag/<tag>
			if len(rest) >= 3 && rest[1] == "tag" {
				repo.Ref = rest[2]
			}
		} else {
			repo.Ref = rest[1]
		}
	}
	if !validName(repo.Owner) || !validName(repo.Name) || (repo.Ref != "" && !validRef(repo.Ref)) {
		return Repo{}, fmt.Errorf("%w: the path has unexpected characters", ErrNotARepo)
	}
	repo.CloneURL = "https://" + host + "/" + repo.Owner + "/" + repo.Name + ".git"
	return repo, nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
var refRe = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)

func validName(s string) bool { return nameRe.MatchString(s) && s != "." && s != ".." }
func validRef(s string) bool {
	return refRe.MatchString(s) && !strings.HasPrefix(s, "-") && !strings.Contains(s, "..")
}

// Options tune a clone. The zero value is the safe default.
type Options struct {
	// Protocols is git's allow-list of transports; "https" when empty. Tests
	// widen it to reach a local repository.
	Protocols string
	// MaxBytes overrides MaxCloneBytes.
	MaxBytes int64
	// Timeout overrides CloneTimeout.
	Timeout time.Duration
}

// ErrNoGit is returned when git is not installed.
var ErrNoGit = errors.New("git is not installed, so repositories cannot be cloned")

// Clone checks out the repository's default branch (or Ref) into dest, which
// must not exist yet.
func Clone(ctx context.Context, repo Repo, dest string, opts Options) error {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return ErrNoGit
	}
	if opts.Protocols == "" {
		opts.Protocols = "https"
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = MaxCloneBytes
	}
	if opts.Timeout <= 0 {
		opts.Timeout = CloneTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	args := []string{
		"-c", "protocol.allow=never",
		"-c", "protocol." + opts.Protocols + ".allow=always",
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.symlinks=false",
		"-c", "core.fsmonitor=false",
		"-c", "advice.detachedHead=false",
		"clone", "--depth", "1", "--single-branch", "--no-tags", "--no-recurse-submodules",
	}
	if repo.Ref != "" {
		args = append(args, "--branch", repo.Ref)
	}
	args = append(args, "--", repo.CloneURL, dest)

	cmd := exec.CommandContext(ctx, gitPath, args...)
	// Nothing from the user's own configuration (credential helpers, URL
	// rewrites, hooks) may influence a clone of someone else's code.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.TempDir(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ASKPASS=true",
		"GIT_LFS_SKIP_SMUDGE=1",
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	// Stop a clone that grows past the ceiling.
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if dirSize(dest) > opts.MaxBytes {
					cancel()
					return
				}
			}
		}
	}()
	err = cmd.Wait()
	close(done)

	if err != nil {
		_ = os.RemoveAll(dest)
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return fmt.Errorf("cloning %s took longer than %s", repo.Display(), opts.Timeout)
		case ctx.Err() != nil:
			return fmt.Errorf("cloning %s was stopped (it is larger than %d MB)", repo.Display(), opts.MaxBytes>>20)
		}
		return fmt.Errorf("git could not clone %s: %s", repo.Display(), stderr.summary())
	}
	return nil
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// limitedBuffer keeps the start of what git prints, which is enough to say why
// it failed.
type limitedBuffer struct{ b []byte }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 2048 - len(l.b); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		l.b = append(l.b, p[:room]...)
	}
	return len(p), nil
}

func (l *limitedBuffer) summary() string {
	s := strings.TrimSpace(string(l.b))
	// Keep the last meaningful line: "fatal: repository not found".
	if lines := strings.Split(s, "\n"); len(lines) > 0 {
		s = strings.TrimSpace(lines[len(lines)-1])
	}
	if s == "" {
		return "unknown error"
	}
	return s
}
