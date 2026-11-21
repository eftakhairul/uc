package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/names"
	"github.com/eftakhairul/uc/internal/registry"
)

// Add implements `uc add <path|url> [name] [--force] [--insecure]`. An
// http(s) source is downloaded instead of copied; both paths share the
// same name validation and overwrite/ambiguity guards.
func (r *Runner) Add(srcPath, name string, force, insecure bool) error {
	if isURL(srcPath) {
		return r.addFromURL(srcPath, name, force, insecure)
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("cannot add '%s': %s", srcPath, reason(err))
	}
	if info.IsDir() {
		return fmt.Errorf("cannot add '%s': it is a directory", srcPath)
	}

	if name == "" {
		name = filepath.Base(srcPath)
	}
	destPath, err := r.prepareAddDest(name, force)
	if err != nil {
		return err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("cannot add '%s': %s", srcPath, reason(err))
	}
	defer src.Close()

	if err := installScript(destPath, src); err != nil {
		return fmt.Errorf("cannot write %s: %s", destPath, reason(err))
	}

	fmt.Fprintf(r.Out, "added %s -> %s\n", srcPath, destPath)
	r.noteShadowedCommand(registry.BareName(name))
	return nil
}

// prepareAddDest validates name, ensures the uc dirs exist, and — unless
// force — refuses to overwrite an existing script, replace an alias or
// function of the same name, or make a bare name ambiguous. It returns
// where the script should be written.
func (r *Runner) prepareAddDest(name string, force bool) (string, error) {
	if err := names.ValidName(name); err != nil {
		return "", err
	}
	bare := registry.BareName(name)

	if err := r.Cfg.EnsureDirs(); err != nil {
		return "", err
	}

	destPath := filepath.Join(r.Cfg.ScriptsDir, name)
	if force {
		return destPath, nil
	}
	if _, err := os.Stat(destPath); err == nil {
		return "", fmt.Errorf("%s already exists — pass --force to overwrite, or remove it with 'uc remove %s'", destPath, bare)
	}
	// A same-named script under another extension would make the bare
	// name ambiguous at invocation time (script resolution tries every
	// known extension) — reject that too.
	existing, err := r.Reg.ResolveScript(bare)
	if err == nil && existing != destPath {
		return "", fmt.Errorf("'%s' would be ambiguous with %s — remove it first, or pass --force", bare, existing)
	}
	if _, ok := errors.AsType[*registry.AmbiguousNameError](err); ok {
		return "", fmt.Errorf("'%s' is already ambiguous — %w", bare, err)
	}
	// An alias or function of the same name would be silently shadowed.
	f, err := r.Reg.Store.Load()
	if err != nil {
		return "", err
	}
	if kind := f.Kind(bare); kind != "" {
		return "", fmt.Errorf("'%s' already exists (%s) — remove it first, or pass --force", bare, kind)
	}
	return destPath, nil
}

// noteShadowedCommand notes when a just-added script hides an alias or
// function (possible with --force).
func (r *Runner) noteShadowedCommand(bare string) {
	f, err := r.Reg.Store.Load()
	if err != nil {
		return
	}
	if kind := f.Kind(bare); kind != "" {
		r.notef("the script shadows the %s '%s' — `uc %s` will run the script", kind, bare, bare)
	}
}

// installScript writes src to destPath with config.ScriptMode via a temp
// file in the same directory plus rename, so a failed copy or download
// never leaves a partial script behind — or destroys the one --force was
// replacing. Chmod (unaffected by umask) sets the mode.
func installScript(destPath string, src io.Reader) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if _, err = io.Copy(tmp, src); err != nil {
		return err
	}
	if err = tmp.Chmod(config.ScriptMode); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), destPath)
}

// maxFetchBytes caps how much `uc add <url>` downloads — scripts are small,
// and anything bigger is almost certainly the wrong URL.
const maxFetchBytes = 1 << 20

// maxRedirects caps how many redirects a download follows.
const maxRedirects = 5

// httpTimeout bounds a whole download. A variable so tests can shorten it.
var httpTimeout = 30 * time.Second

// httpTransport is the download transport; nil means the default. Tests
// set it to trust a TLS test server.
var httpTransport http.RoundTripper

var (
	errTooManyRedirects = errors.New("too many redirects")
	errHTTPRedirect     = errors.New("redirected to plain http — pass --insecure to allow")
)

// newHTTPClient builds the download client. Redirects are followed
// (gist.github.com/.../raw redirects to gist.githubusercontent.com) up to
// maxRedirects, but — unless insecure — never from https down to plain
// http, which would let anyone on the network path swap the script.
func newHTTPClient(insecure bool) *http.Client {
	return &http.Client{
		Timeout:   httpTimeout,
		Transport: httpTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errTooManyRedirects
			}
			if !insecure && req.URL.Scheme == "http" && !isLoopback(req.URL.Hostname()) {
				return errHTTPRedirect
			}
			return nil
		},
	}
}

// isLoopback reports whether host is this machine, where plain http can't
// be tampered with in transit.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isURL reports whether an `uc add` source should be downloaded rather
// than read from disk.
func isURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// rawURL turns a user-supplied URL into the one to download: a
// gist.github.com page URL gets "/raw" appended (which serves the gist's
// first file); every other http(s) URL passes through unchanged.
func rawURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid URL '%s' — want an https:// URL with a host", raw)
	}
	if strings.EqualFold(u.Hostname(), "gist.github.com") &&
		!strings.HasSuffix(u.Path, "/raw") && !strings.Contains(u.Path, "/raw/") {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/raw"
		u.RawPath = ""
	}
	return u.String(), nil
}

// nameFromURL derives a script name from the last path segment of a
// (rewritten) URL. Query strings and fragments are ignored by construction.
func nameFromURL(fetchURL string) (string, error) {
	u, err := url.Parse(fetchURL)
	if err != nil {
		return "", err
	}
	name := path.Base(u.Path)
	switch name {
	case "", "/", ".", "raw":
		return "", fmt.Errorf("cannot derive a name from this URL, pass one explicitly: uc add <url> <name>")
	}
	return name, nil
}

// fetchScript downloads fetchURL, requiring a 200 response and a non-empty
// body no larger than maxFetchBytes that isn't a web page.
func fetchScript(client *http.Client, fetchURL string) ([]byte, error) {
	resp, err := client.Get(fetchURL)
	if err != nil {
		// *url.Error repeats the method and URL; unwrap it so the URL
		// appears once.
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			if uerr.Timeout() {
				return nil, fmt.Errorf("cannot download %s: timed out", fetchURL)
			}
			err = uerr.Err
		}
		return nil, fmt.Errorf("cannot download %s: %v", fetchURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cannot download %s: the server answered %s", fetchURL, resp.Status)
	}
	// Read one byte past the cap so an oversized body is an error rather
	// than a silently truncated (and likely broken) script.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %v", fetchURL, err)
	}
	if len(body) > maxFetchBytes {
		return nil, fmt.Errorf("cannot download %s: the response is larger than %d bytes", fetchURL, maxFetchBytes)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("cannot download %s: empty response", fetchURL)
	}
	if isHTML(body) {
		return nil, fmt.Errorf("this URL returned a web page, not a script — use the raw file URL")
	}
	return body, nil
}

// isHTML reports whether body looks like a web page rather than a script.
func isHTML(body []byte) bool {
	head := bytes.TrimLeft(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), " \t\r\n")
	head = head[:min(len(head), 16)]
	lower := strings.ToLower(string(head))
	return strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html")
}

// addFromURL implements the URL form of `uc add`. Plain http is refused
// unless insecure (or the host is this machine). The name and overwrite
// guards run before the download, so a doomed add costs no network round
// trip, and nothing is written unless the fetch fully succeeds.
func (r *Runner) addFromURL(rawurl, name string, force, insecure bool) error {
	fetchURL, err := rawURL(rawurl)
	if err != nil {
		return err
	}
	if u, _ := url.Parse(fetchURL); u.Scheme == "http" && !insecure && !isLoopback(u.Hostname()) {
		return errors.New("refusing to download over plain http — use an https URL, or pass --insecure")
	}
	if name == "" {
		if name, err = nameFromURL(fetchURL); err != nil {
			return err
		}
	}
	destPath, err := r.prepareAddDest(name, force)
	if err != nil {
		return err
	}

	body, err := fetchScript(newHTTPClient(insecure), fetchURL)
	if err != nil {
		return err
	}
	if err := installScript(destPath, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("cannot write %s: %s", destPath, reason(err))
	}

	fmt.Fprintf(r.Out, "added %s -> %s\n", rawurl, destPath)
	fmt.Fprintf(r.Out, "note: review it before first run — uc which %s\n", registry.BareName(name))
	r.noteShadowedCommand(registry.BareName(name))
	return nil
}

// scriptTemplate is the scaffold `uc new` writes for one --lang value.
type scriptTemplate struct {
	lang    string
	ext     string
	shebang string
	// preamble is language-specific boilerplate below the metadata block.
	preamble string
}

// templates lists the supported --lang values, in the order shown in usage
// and error text. All five languages use "#" comments, so the metadata block
// ExtractMeta reads back is identical across them — only the shebang and
// preamble differ. A slice (not a map) keeps that order stable.
var templates = []scriptTemplate{
	{lang: "sh", ext: ".sh", shebang: "#!/usr/bin/env bash", preamble: "set -euo pipefail\n"},
	{lang: "py", ext: ".py", shebang: "#!/usr/bin/env python3"},
	{lang: "js", ext: ".js", shebang: "#!/usr/bin/env node"},
	{lang: "rb", ext: ".rb", shebang: "#!/usr/bin/env ruby"},
	{lang: "pl", ext: ".pl", shebang: "#!/usr/bin/env perl"},
}

func lookupTemplate(lang string) (scriptTemplate, bool) {
	for _, t := range templates {
		if t.lang == lang {
			return t, true
		}
	}
	return scriptTemplate{}, false
}

// langList renders the valid --lang values for usage and error messages.
func langList() string {
	ls := make([]string, len(templates))
	for i, t := range templates {
		ls[i] = t.lang
	}
	return strings.Join(ls, "|")
}

// render fills the template for the name the script will be invoked by.
func (t scriptTemplate) render(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", t.shebang)
	b.WriteString("# @desc:\n")
	fmt.Fprintf(&b, "# @usage: %s <args>\n", name)
	fmt.Fprintf(&b, "# @example: %s\n\n", name)
	if t.preamble != "" {
		fmt.Fprintf(&b, "%s\n", t.preamble)
	}
	return b.String()
}

// New implements `uc new <name> [--lang sh|py|js|rb|pl]` — writes a language
// template straight into the scripts directory and opens $EDITOR on it, so
// saving the file is what registers it. The editor runs as a child process
// (not process replacement) because uc needs control back to decide whether
// anything was actually written: an untouched template counts as an abort
// and leaves nothing behind, mirroring `git commit` with an unedited message.
func (r *Runner) New(name, lang string) error {
	if err := names.ValidName(name); err != nil {
		return err
	}
	bare := registry.BareName(name)
	tmpl, ok := lookupTemplate(lang)
	if !ok {
		return fmt.Errorf("unknown --lang '%s' (want %s)", lang, langList())
	}

	// Any existing resolution blocks: a script under another extension, an
	// alias, or a function would shadow the new file or be shadowed by it.
	// Not-found is the only good outcome here; an ambiguous name (or a
	// broken store) is surfaced as-is.
	if resolved, err := r.Reg.Resolve(bare); err == nil {
		return fmt.Errorf("'%s' already exists (%s) — edit it with 'uc edit %s'", bare, resolved.Kind, bare)
	} else if _, notFound := errors.AsType[*registry.NotFoundError](err); !notFound {
		return err
	}

	if err := r.Cfg.EnsureDirs(); err != nil {
		return err
	}
	filename := name + tmpl.ext
	if filepath.Ext(name) == tmpl.ext {
		// `uc new killport.sh` shouldn't produce killport.sh.sh.
		filename = name
	}
	destPath := filepath.Join(r.Cfg.ScriptsDir, filename)

	initial := tmpl.render(bare)
	if err := os.WriteFile(destPath, []byte(initial), config.ScriptMode); err != nil {
		return fmt.Errorf("cannot create %s: %s", destPath, reason(err))
	}
	// WriteFile's mode is subject to umask, so set it explicitly (same as
	// Add) — extensionless scripts are run directly.
	if err := os.Chmod(destPath, config.ScriptMode); err != nil {
		return fmt.Errorf("cannot create %s: %s", destPath, reason(err))
	}

	if err := r.runEditor(destPath); err != nil {
		os.Remove(destPath)
		if errors.Is(err, errEditorFailed) {
			return errors.New("editor exited with an error, nothing registered")
		}
		return fmt.Errorf("%w — nothing registered", err)
	}

	edited, err := os.ReadFile(destPath)
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("cannot read %s: %s", destPath, reason(err))
	}
	if string(edited) == initial {
		if err := os.Remove(destPath); err != nil {
			return fmt.Errorf("cannot remove %s: %s", destPath, reason(err))
		}
		fmt.Fprintln(r.Out, "aborted, nothing registered")
		return nil
	}

	fmt.Fprintf(r.Out, "created %s — run it with: uc %s\n", destPath, bare)
	return nil
}
