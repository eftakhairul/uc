//go:build e2e && windows

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uc.exe runs natively on the same host as the test, so an in-process
// httptest server stands in for the remote host — no network needed.
func TestAddFromURL(t *testing.T) {
	requireBash(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/tools/killport.sh":
			fmt.Fprint(w, "#!/bin/bash\n# @desc: kills a port (remote)\necho remote kill port $1\n")
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	env := sandbox(t)

	// The .sh extension carried by the URL is what routes the script to
	// bash on Windows, as with a local add.
	out, code := runUC(t, env, "add", srv.URL+"/tools/killport.sh")
	require.Equalf(t, 0, code, "add url: output:\n%s", out)
	assert.Contains(t, out, "added", "add output")
	assert.Contains(t, out, "review it before first run", "add output")

	out, code = runUC(t, env, "list")
	require.Zerof(t, code, "list output:\n%s", out)
	assert.Contains(t, out, "kills a port (remote)", "list output")

	out, code = runUC(t, env, "killport", "8080")
	require.Zerof(t, code, "run output:\n%s", out)
	assert.Contains(t, out, "remote kill port 8080", "run output")

	// A failed fetch registers nothing.
	out, code = runUC(t, env, "add", srv.URL+"/tools/missing.sh")
	assert.Equalf(t, 1, code, "404 add: output:\n%s", out)
	assert.Contains(t, out, "404", "404 add output")

	// sandbox appends its UC_HOME last, so the last match is the one uc saw.
	var ucHome string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "UC_HOME="); ok {
			ucHome = v
		}
	}
	_, err := os.Stat(filepath.Join(ucHome, "scripts", "missing.sh"))
	assert.True(t, os.IsNotExist(err), "404 add must not leave a file behind")
}
