package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/picker"
	"github.com/eftakhairul/uc/internal/registry"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uc: %v\n", err)
		os.Exit(1)
	}

	r := commands.New(cfg)
	args := os.Args[1:]

	if len(args) == 0 {
		if err := picker.Run(cfg, r); err != nil {
			fmt.Fprintf(os.Stderr, "uc: %v\n", err)
			os.Exit(1)
		}
		return
	}

	name, rest := args[0], args[1:]

	var cmdErr error
	switch name {
	case "list", "ls":
		cmdErr = r.List()
	case "add":
		cmdErr = runAdd(r, rest)
	case "remove", "rm":
		cmdErr = runRemove(r, rest)
	case "which":
		cmdErr = runWhich(r, rest)
	case "edit":
		cmdErr = runEdit(r, rest)
	case "completion":
		cmdErr = runCompletion(r, rest)
	case "history":
		cmdErr = runHistory(r, rest)
	case "alias":
		cmdErr = runAlias(r, rest)
	case "function":
		cmdErr = runFunction(r, rest)
	case "help":
		cmdErr = runHelp(r, rest)
	case "version":
		cmdErr = r.PrintVersion()
	case "__complete":
		cmdErr = runComplete(r, rest)
	default:
		// `uc <name> --help` / `uc <name> -h` is a shortcut for
		// `uc help <name>`, but only when it's the sole argument after
		// <name> — otherwise a script wanting to handle its own --help
		// flag (possibly alongside others) would never see it
		// (architecture §3.5).
		if len(rest) == 1 && (rest[0] == "--help" || rest[0] == "-h") {
			cmdErr = r.Help(name)
		} else {
			cmdErr = r.Run(name, rest)
		}
	}

	if cmdErr != nil {
		exitCode := 1
		if _, ok := errors.AsType[*registry.NotFoundError](cmdErr); ok {
			exitCode = 127
		}
		fmt.Fprintf(os.Stderr, "uc: %v\n", cmdErr)
		os.Exit(exitCode)
	}
}

func runAdd(r *commands.Runner, args []string) error {
	force := false
	var rest []string
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) < 1 || len(rest) > 2 {
		return fmt.Errorf("usage: uc add <path> [name] [--force]")
	}
	name := ""
	if len(rest) == 2 {
		name = rest[1]
	}
	return r.Add(rest[0], name, force)
}

func runRemove(r *commands.Runner, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: uc remove <name>")
	}
	return r.Remove(args[0])
}

func runWhich(r *commands.Runner, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: uc which <name>")
	}
	return r.Which(args[0])
}

func runEdit(r *commands.Runner, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: uc edit <name>")
	}
	return r.Edit(args[0])
}

func runCompletion(r *commands.Runner, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: uc completion <bash|zsh>")
	}
	return r.Completion(args[0])
}

func runComplete(r *commands.Runner, args []string) error {
	partial := ""
	if len(args) >= 1 {
		partial = args[0]
	}
	return r.Complete(partial)
}

func runHistory(r *commands.Runner, args []string) error {
	if len(args) >= 1 && args[0] == "run" {
		if len(args) != 2 {
			return fmt.Errorf("usage: uc history run <n>")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid history index %q: %w", args[1], err)
		}
		return r.HistoryRun(n)
	}

	n := 0 // 0 = let History apply the configured default
	if len(args) >= 1 {
		v, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid count %q: %w", args[0], err)
		}
		n = v
	}
	return r.History(n)
}

func runAlias(r *commands.Runner, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: uc alias <add|remove|list|edit> ...")
	}
	switch args[0] {
	case "add":
		return runAliasAdd(r, args[1:])
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: uc alias remove <name>")
		}
		return r.AliasRemove(args[1])
	case "list":
		return r.AliasList()
	case "edit":
		if len(args) != 2 {
			return fmt.Errorf("usage: uc alias edit <name>")
		}
		return r.AliasEdit(args[1])
	default:
		return fmt.Errorf("usage: uc alias <add|remove|list|edit> ...")
	}
}

func runAliasAdd(r *commands.Runner, args []string) error {
	usage := fmt.Errorf(`usage: uc alias add <name> <command...> [--desc "..."]`)
	if len(args) < 2 {
		return usage
	}
	name, desc := args[0], ""
	var words []string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--desc" && i+1 < len(rest) {
			desc = rest[i+1]
			i++
			continue
		}
		words = append(words, rest[i])
	}
	if len(words) == 0 {
		return usage
	}
	return r.AliasAdd(name, strings.Join(words, " "), desc)
}

func runFunction(r *commands.Runner, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: uc function <add|remove|list|edit> ...")
	}
	switch args[0] {
	case "add":
		return runFunctionAdd(r, args[1:])
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: uc function remove <name>")
		}
		return r.FunctionRemove(args[1])
	case "list":
		return r.FunctionList()
	case "edit":
		if len(args) != 2 {
			return fmt.Errorf("usage: uc function edit <name>")
		}
		return r.FunctionEdit(args[1])
	default:
		return fmt.Errorf("usage: uc function <add|remove|list|edit> ...")
	}
}

func runFunctionAdd(r *commands.Runner, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf(`usage: uc function add <name> <body> [--desc "..."]`)
	}
	name, body := args[0], args[1]
	desc := ""
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--desc" && i+1 < len(rest) {
			desc = rest[i+1]
			i++
		}
	}
	return r.FunctionAdd(name, body, desc)
}

func runHelp(r *commands.Runner, args []string) error {
	arg := ""
	if len(args) >= 1 {
		arg = args[0]
	}
	return r.Help(arg)
}
