package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/help"
	"github.com/eftakhairul/uc/internal/picker"
	"github.com/eftakhairul/uc/internal/registry"
	"github.com/eftakhairul/uc/internal/reserved"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is main without the exit, so tests can drive the whole CLI. On unix a
// successful script run never returns: the executor replaces the process.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "uc: %v\n", err)
		return 1
	}
	// Tab completion must stay silent, whatever is wrong with settings or
	// commands.json: anything on stderr lands in the user's prompt line.
	completing := len(args) > 0 && args[0] == "__complete"
	if !completing {
		for _, w := range cfg.Warnings {
			fmt.Fprintf(stderr, "uc: warning: %s\n", w)
		}
	}

	r := commands.New(cfg)
	r.Out, r.Err, r.In = stdout, stderr, stdin
	cfg.Store().Warn = stderr
	if completing {
		cfg.Store().Warn = io.Discard
	}

	return report(stderr, dispatch(cfg, r, args))
}

// report prints err and returns the exit code: 0 for success, 2 for a
// usage error, 127 for an unknown name, 1 for anything else.
func report(stderr io.Writer, err error) int {
	if err == nil {
		return 0
	}
	if ue, ok := errors.AsType[*usageError](err); ok {
		fmt.Fprintf(stderr, "uc: %s\n", ue.msg)
		if u, ok := help.Usage(ue.cmd); ok {
			fmt.Fprintln(stderr, u)
		}
		if ue.cmd == "" {
			fmt.Fprintln(stderr, "See 'uc help'.")
		} else {
			fmt.Fprintf(stderr, "See 'uc help %s'.\n", ue.cmd)
		}
		return 2
	}
	fmt.Fprintf(stderr, "uc: %v\n", err)
	if _, ok := errors.AsType[*registry.NotFoundError](err); ok {
		return 127
	}
	return 1
}

// isHelpFlag reports whether rest is exactly one help flag.
func isHelpFlag(rest []string) bool {
	return len(rest) == 1 && (rest[0] == "-h" || rest[0] == "--help")
}

func dispatch(cfg *config.Config, r *commands.Runner, args []string) error {
	if len(args) == 0 {
		return picker.Run(cfg, r)
	}
	name, rest := args[0], args[1:]

	// `uc <command> --help` is `uc help <command>` — only as the sole
	// argument, the same rule scripts follow below.
	if reserved.Is(name) && !reserved.Hidden[name] && isHelpFlag(rest) {
		return r.Help(name)
	}
	r.WarnShadowed(name)

	switch name {
	case "-h", "--help":
		if err := wantArgs("", rest, 0, 0); err != nil {
			return err
		}
		return r.Help("")
	case "-v", "--version":
		if err := wantArgs("", rest, 0, 0); err != nil {
			return err
		}
		return r.PrintVersion()
	case "list", "ls", "version":
		if err := noArgs(name, rest); err != nil {
			return err
		}
		if name == "version" {
			return r.PrintVersion()
		}
		return r.List()
	case "add":
		return runAdd(r, rest)
	case "new":
		return runNew(r, rest)
	case "remove", "rm", "which", "edit":
		return runOneName(r, name, rest)
	case "completion":
		return runCompletion(r, rest)
	case "history":
		return runHistory(r, rest)
	case "alias":
		return runAlias(r, rest)
	case "function":
		return runFunction(r, rest)
	case "sync":
		return runSync(r, rest)
	case "export":
		return runExport(r, rest)
	case "import":
		return runImport(r, rest)
	case "help":
		return runHelp(r, rest)
	case "__complete":
		return runComplete(r, rest)
	}

	if isFlag(name) || name == "-" {
		return usagef("", "unknown option '%s'", name)
	}
	// `uc <name> --help` / `uc <name> -h` is a shortcut for `uc help
	// <name>`, but only when it's the sole argument after <name> —
	// otherwise a script wanting to handle its own --help flag (possibly
	// alongside others) would never see it (architecture §3.5).
	if isHelpFlag(rest) {
		return r.Help(name)
	}
	return r.Run(name, rest)
}

// noArgs rejects any argument, flag or not.
func noArgs(cmd string, args []string) error {
	_, pos, err := splitFlags(cmd, args, nil)
	if err != nil {
		return err
	}
	return wantArgs(cmd, pos, 0, 0)
}

func runOneName(r *commands.Runner, cmd string, args []string) error {
	_, pos, err := splitFlags(cmd, args, nil)
	if err != nil {
		return err
	}
	if err := wantArgs(cmd, pos, 1, 1); err != nil {
		return err
	}
	switch cmd {
	case "which":
		return r.Which(pos[0])
	case "edit":
		return r.Edit(pos[0])
	default:
		return r.Remove(pos[0])
	}
}

func runAdd(r *commands.Runner, args []string) error {
	flags, pos, err := splitFlags("add", args, flagSpec{"--force": false, "--insecure": false})
	if err != nil {
		return err
	}
	if err := wantArgs("add", pos, 1, 2); err != nil {
		return err
	}
	name := ""
	if len(pos) == 2 {
		name = pos[1]
	}
	return r.Add(pos[0], name, flagSet(flags, "--force"), flagSet(flags, "--insecure"))
}

func runNew(r *commands.Runner, args []string) error {
	flags, pos, err := splitFlags("new", args, flagSpec{"--lang": true})
	if err != nil {
		return err
	}
	if err := wantArgs("new", pos, 1, 1); err != nil {
		return err
	}
	lang := "sh"
	if v, ok := flags["--lang"]; ok {
		lang = v
	}
	return r.New(pos[0], lang)
}

func runCompletion(r *commands.Runner, args []string) error {
	_, pos, err := splitFlags("completion", args, nil)
	if err != nil {
		return err
	}
	if err := wantArgs("completion", pos, 1, 1); err != nil {
		return err
	}
	switch pos[0] {
	case "bash", "zsh", "fish":
		return r.Completion(pos[0])
	default:
		return usagef("completion", "unsupported shell '%s' (want bash, zsh, or fish)", pos[0])
	}
}

func runComplete(r *commands.Runner, args []string) error {
	describe := false
	if len(args) >= 1 && args[0] == "--describe" {
		describe = true
		args = args[1:]
	}
	partial := ""
	if len(args) >= 1 {
		partial = args[0]
	}
	return r.Complete(partial, describe)
}

// runHistory takes `uc history [<n>]`, `uc history run <n>`, and
// `uc history clear`. Anything else in the first position must be a
// positive number.
func runHistory(r *commands.Runner, args []string) error {
	const cmd = "history"
	if len(args) >= 1 {
		switch args[0] {
		case "run":
			if err := wantArgs(cmd, args[1:], 1, 1); err != nil {
				return err
			}
			n, err := positiveInt(cmd, "history run", args[1])
			if err != nil {
				return err
			}
			return r.HistoryRun(n)
		case "clear":
			if err := wantArgs(cmd, args[1:], 0, 0); err != nil {
				return err
			}
			return r.HistoryClear()
		}
	}
	if err := wantArgs(cmd, args, 0, 1); err != nil {
		return err
	}
	n := 0 // 0 = let History apply the configured default
	if len(args) == 1 {
		v, err := positiveInt(cmd, cmd, args[0])
		if err != nil {
			return err
		}
		n = v
	}
	return r.History(n)
}

// verb splits off a family's verb, rejecting a missing or unknown one.
func verb(cmd string, args []string, verbs ...string) (string, []string, error) {
	list := strings.Join(verbs[:len(verbs)-1], ", ") + ", or " + verbs[len(verbs)-1]
	if len(args) == 0 {
		return "", nil, usagef(cmd, "'%s' needs a command: %s", cmd, list)
	}
	for _, v := range verbs {
		if args[0] == v {
			return v, args[1:], nil
		}
	}
	return "", nil, usagef(cmd, "unknown %s command '%s'", cmd, args[0])
}

func runAlias(r *commands.Runner, args []string) error {
	v, rest, err := verb("alias", args, "add", "remove", "list", "edit")
	if err != nil {
		return err
	}
	if v == "add" {
		name, words, desc, literal, err := parseAliasAdd(rest)
		if err != nil {
			return err
		}
		return r.AliasAddWords(name, words, desc, literal)
	}
	_, pos, err := splitFlags("alias", rest, nil)
	if err != nil {
		return err
	}
	switch v {
	case "list":
		if err := wantArgs("alias", pos, 0, 0); err != nil {
			return err
		}
		return r.AliasList()
	case "edit":
		if err := wantArgs("alias", pos, 1, 1); err != nil {
			return err
		}
		return r.AliasEdit(pos[0])
	default:
		if err := wantArgs("alias", pos, 1, 1); err != nil {
			return err
		}
		return r.AliasRemove(pos[0])
	}
}

func runFunction(r *commands.Runner, args []string) error {
	const cmd = "function"
	v, rest, err := verb(cmd, args, "add", "remove", "list", "edit")
	if err != nil {
		return err
	}
	spec := flagSpec(nil)
	if v == "add" {
		spec = flagSpec{"--desc": true}
	}
	flags, pos, err := splitFlags(cmd, rest, spec)
	if err != nil {
		return err
	}
	switch v {
	case "add":
		if len(pos) > 2 {
			return usagef(cmd, "the function body must be one argument — wrap it in quotes")
		}
		if err := wantArgs(cmd, pos, 2, 2); err != nil {
			return err
		}
		return r.FunctionAdd(pos[0], pos[1], flags["--desc"])
	case "list":
		if err := wantArgs(cmd, pos, 0, 0); err != nil {
			return err
		}
		return r.FunctionList()
	case "edit":
		if err := wantArgs(cmd, pos, 1, 1); err != nil {
			return err
		}
		return r.FunctionEdit(pos[0])
	default:
		if err := wantArgs(cmd, pos, 1, 1); err != nil {
			return err
		}
		return r.FunctionRemove(pos[0])
	}
}

func runSync(r *commands.Runner, args []string) error {
	const cmd = "sync"
	v, rest, err := verb(cmd, args, "init", "push", "pull", "status")
	if err != nil {
		return err
	}
	spec := flagSpec(nil)
	if v == "pull" {
		spec = flagSpec{"--force": false}
	}
	flags, pos, err := splitFlags(cmd, rest, spec)
	if err != nil {
		return err
	}
	switch v {
	case "init":
		if err := wantArgs(cmd, pos, 1, 1); err != nil {
			return err
		}
		return r.SyncInit(pos[0])
	case "push":
		if err := wantArgs(cmd, pos, 0, 0); err != nil {
			return err
		}
		return r.SyncPush()
	case "pull":
		if err := wantArgs(cmd, pos, 0, 0); err != nil {
			return err
		}
		return r.SyncPull(flagSet(flags, "--force"))
	default:
		if err := wantArgs(cmd, pos, 0, 0); err != nil {
			return err
		}
		return r.SyncStatus()
	}
}

func runExport(r *commands.Runner, args []string) error {
	flags, pos, err := splitFlags("export", args, flagSpec{"--force": false})
	if err != nil {
		return err
	}
	if err := wantArgs("export", pos, 0, 1); err != nil {
		return err
	}
	path := ""
	if len(pos) == 1 {
		path = pos[0]
	}
	return r.Export(path, flagSet(flags, "--force"))
}

func runImport(r *commands.Runner, args []string) error {
	flags, pos, err := splitFlags("import", args, flagSpec{"--force": false, "--dry-run": false})
	if err != nil {
		return err
	}
	if err := wantArgs("import", pos, 1, 1); err != nil {
		return err
	}
	return r.Import(pos[0], flagSet(flags, "--force"), flagSet(flags, "--dry-run"))
}

func runHelp(r *commands.Runner, args []string) error {
	if err := wantArgs("help", args, 0, 1); err != nil {
		return err
	}
	if len(args) == 0 {
		return r.Help("")
	}
	return r.Help(args[0])
}
