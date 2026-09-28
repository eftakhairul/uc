//go:build e2e && !windows

package e2e

import (
	"path"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHTTPBase is fakehttp's address as seen from inside the container.
const fakeHTTPBase = "http://127.0.0.1"

// stageRemote publishes content at fakeHTTPBase/<relPath> by writing it
// under fakehttp's root, and returns the URL. relPath should be unique per
// test, since the served tree is shared across the container.
func stageRemote(t *testing.T, relPath, content string) string {
	t.Helper()
	full := path.Join(fakeHTTPRoot, relPath)
	_, code := execIn(t, nil, "mkdir", "-p", path.Dir(full))
	require.Zerof(t, code, "mkdir %s: nonzero exit", path.Dir(full))
	writeFile(t, full, content)
	return fakeHTTPBase + "/" + relPath
}

func TestAddFromURLLifecycle(t *testing.T) {
	env := sandbox(t)
	url := stageRemote(t, "lifecycle/killport.sh",
		"#!/bin/sh\n# @desc: kills a port (remote)\necho \"remote killport $1\"\n")

	out, code := runUC(t, env, "add", url)
	require.Equalf(t, 0, code, "add url: output:\n%s", out)
	assert.Contains(t, out, "added "+url+" -> ", "add output")
	assert.Contains(t, out, "review it before first run", "add output carries the review note")
	assert.Contains(t, out, "uc which killport", "review note names the script")

	out, code = runUC(t, env, "list")
	require.Zerof(t, code, "list output:\n%s", out)
	assert.Contains(t, out, "killport", "list output")
	assert.Contains(t, out, "kills a port (remote)", "list shows the downloaded @desc")

	out, code = runUC(t, env, "which", "killport")
	require.Zerof(t, code, "which output:\n%s", out)
	assert.Contains(t, out, "/uc-home/scripts/killport.sh", "which output")

	// Installed executable, and runs through the normal dispatch path.
	out, code = execIn(t, env, "sh", "-c", `test -x "$UC_HOME/scripts/killport.sh"`)
	assert.Zerof(t, code, "downloaded script must be executable: %s", out)

	out, code = runUC(t, env, "killport", "8080")
	require.Zerof(t, code, "run output:\n%s", out)
	assert.Contains(t, out, "remote killport 8080", "run output")
}

func TestAddFromURLExplicitNameAndForce(t *testing.T) {
	env := sandbox(t)
	v1 := stageRemote(t, "force/v1/tool.sh", "#!/bin/sh\necho v1\n")
	v2 := stageRemote(t, "force/v2/tool.sh", "#!/bin/sh\necho v2\n")

	out, code := runUC(t, env, "add", v1, "mytool")
	require.Equalf(t, 0, code, "add v1: output:\n%s", out)
	assert.Contains(t, out, "uc which mytool", "explicit name is used")

	// A URL install is not a licence to clobber.
	out, code = runUC(t, env, "add", v2, "mytool")
	assert.Equalf(t, 1, code, "duplicate add: output:\n%s", out)
	assert.Contains(t, out, "already exists", "duplicate add output")

	out, code = runUC(t, env, "mytool")
	require.Zerof(t, code, "run after refused overwrite:\n%s", out)
	assert.Contains(t, out, "v1", "refused overwrite leaves the original")

	out, code = runUC(t, env, "add", v2, "mytool", "--force")
	require.Equalf(t, 0, code, "force add: output:\n%s", out)

	out, code = runUC(t, env, "mytool")
	require.Zerof(t, code, "run after force:\n%s", out)
	assert.Contains(t, out, "v2", "--force replaced the script")
}

func TestAddFromURLFollowsRedirect(t *testing.T) {
	env := sandbox(t)
	stageRemote(t, "redirected/moved.sh", "#!/bin/sh\necho moved\n")

	out, code := runUC(t, env, "add", fakeHTTPBase+"/redirect/redirected/moved.sh")
	require.Equalf(t, 0, code, "add redirecting url: output:\n%s", out)

	out, code = runUC(t, env, "moved")
	require.Zerof(t, code, "run output:\n%s", out)
	assert.Contains(t, out, "moved", "redirect target was installed")
}

func TestAddFromURLFailures(t *testing.T) {
	env := sandbox(t)
	stageRemote(t, "failures/empty.sh", "")

	for _, tc := range []struct {
		name    string
		args    []string
		wantOut string
	}{
		{"not found", []string{"add", fakeHTTPBase + "/failures/missing.sh"}, "404"},
		{"server error", []string{"add", fakeHTTPBase + "/status/500", "boom"}, "500"},
		{"empty body", []string{"add", fakeHTTPBase + "/failures/empty.sh"}, "empty response"},
		{"underivable name", []string{"add", fakeHTTPBase + "/"}, "cannot derive a name"},
		{"reserved name", []string{"add", fakeHTTPBase + "/failures/list"}, "reserved"},
		{"traversal name", []string{"add", fakeHTTPBase + "/failures/x.sh", "../evil"}, "invalid script name"},
		{"connection refused", []string{"add", "http://127.0.0.1:1/tool.sh"}, "fetch"},
		{"no host", []string{"add", "https://"}, "invalid URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runUC(t, env, tc.args...)
			assert.Equalf(t, 1, code, "output:\n%s", out)
			assert.Contains(t, out, tc.wantOut)
			assert.NotContains(t, out, "added", "a failed add must not report success")
		})
	}

	// None of the failures left anything registered.
	out, code := execIn(t, env, "sh", "-c", `ls -A "$UC_HOME/scripts" 2>/dev/null`)
	assert.Emptyf(t, out, "scripts dir must be empty after failed adds (exit %d)", code)
}

func TestAddFromURLSizeCap(t *testing.T) {
	env := sandbox(t)
	full := path.Join(fakeHTTPRoot, "sizecap/big.sh")
	_, code := execIn(t, nil, "sh", "-c",
		`mkdir -p "$(dirname "$1")" && { printf '#!/bin/sh\n'; head -c 1048576 /dev/zero | tr '\0' '#'; } > "$1"`,
		"sh", full)
	require.Zero(t, code, "stage oversized script")

	out, code := runUC(t, env, "add", fakeHTTPBase+"/sizecap/big.sh")
	assert.Equalf(t, 1, code, "oversized add: output:\n%s", out)
	assert.Contains(t, out, "larger than", "oversized add output")
}

func TestAddFromGistURL(t *testing.T) {
	env := sandbox(t)

	// Point gist.github.com at fakehttp so the real gist rewrite
	// (page URL -> /raw) runs end to end. uc is built with CGO_ENABLED=0,
	// so Go's resolver reads /etc/hosts directly.
	_, code := execIn(t, nil, "sh", "-c",
		`grep -q ' gist.github.com$' /etc/hosts || echo '127.0.0.1 gist.github.com' >> /etc/hosts`)
	require.Zero(t, code, "map gist.github.com to fakehttp")

	// FileServer serves <root>/someone/abc123/raw for GET /someone/abc123/raw.
	stageRemote(t, "someone/abc123/raw", "#!/bin/sh\necho from gist\n")

	// A gist page URL derives the name "raw", which is refused.
	out, code := runUC(t, env, "add", "http://gist.github.com/someone/abc123")
	assert.Equalf(t, 1, code, "gist add without name: output:\n%s", out)
	assert.Contains(t, out, "cannot derive a name", "gist add without name")

	out, code = runUC(t, env, "add", "http://gist.github.com/someone/abc123", "gisty")
	require.Equalf(t, 0, code, "gist add: output:\n%s", out)
	assert.Contains(t, out, "added http://gist.github.com/someone/abc123 -> ", "output shows the URL as typed")

	out, code = runUC(t, env, "gisty")
	require.Zerof(t, code, "run gisty:\n%s", out)
	assert.Contains(t, out, "from gist", "the /raw rewrite fetched the gist content")
}

func TestHelpAddDescribesURLForm(t *testing.T) {
	env := sandbox(t)

	out, code := runUC(t, env, "help", "add")
	require.Zerof(t, code, "help add output:\n%s", out)
	assert.Contains(t, out, "<path|url>", "help add")
	assert.Contains(t, out, "gist", "help add")

	out, code = runUC(t, env, "add")
	assert.Equalf(t, 1, code, "bare add output:\n%s", out)
	assert.Contains(t, out, "usage: uc add <path|url> [name] [--force]", "bare add usage")
}
