//go:build e2e

// Command fakehttp is the e2e suite's stand-in for the internet: a static
// file server that runs as the test container's main process, so
// `uc add <url>` can be exercised end to end without network access.
//
// Routes:
//
//	/redirect/<path>  302 to /<path>
//	/status/<code>    empty response with that status code
//	anything else     the file at <root>/<path> (404 if missing)
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

func main() {
	addr := flag.String("addr", ":80", "listen address")
	root := flag.String("root", "/srv/uc-e2e", "directory to serve")
	flag.Parse()

	if err := os.MkdirAll(*root, 0o755); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/redirect/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, strings.TrimPrefix(req.URL.Path, "/redirect"), http.StatusFound)
	})
	mux.HandleFunc("/status/", func(w http.ResponseWriter, req *http.Request) {
		code, err := strconv.Atoi(strings.TrimPrefix(req.URL.Path, "/status/"))
		if err != nil || code < 100 || code > 999 {
			http.Error(w, "bad status code", http.StatusBadRequest)
			return
		}
		w.WriteHeader(code)
	})
	mux.Handle("/", http.FileServer(http.Dir(*root)))

	// The readiness line the e2e TestMain waits for.
	fmt.Printf("fakehttp: listening on %s serving %s\n", *addr, *root)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
