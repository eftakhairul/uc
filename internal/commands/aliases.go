package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eftakhairul/uc/internal/names"
	"github.com/eftakhairul/uc/internal/registry"
	"github.com/eftakhairul/uc/internal/store"
)

// AliasAdd implements the core of `uc alias add` — command is arbitrary
// shell text, the same idea as a bash alias (e.g. `alias gs='git status'`);
// it isn't required to be a registered uc script (architecture §3.2b).
func (r *Runner) AliasAdd(name, command, desc string) error {
	if err := names.ValidName(name); err != nil {
		return err
	}
	if strings.TrimSpace(command) == "" {
		return errors.New("the alias command cannot be empty")
	}
	if err := r.Reg.Store.SetAlias(name, store.Alias{Command: command, Desc: desc}); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "added alias %s -> %s\n", name, names.EscapeControl(command))
	r.warnIfAppendHazard(command)
	r.warnIfShadowed(name, registry.KindAlias)
	return nil
}

// AliasAddWords is `uc alias add` as typed: words are the command's words
// (joined with single spaces), and literal reports whether the user put
// "--" before them. Under the strict rule, uc's options go before the
// command, so a "--desc" among the words is part of the command; when that
// looks like an old-style trailing description, a note says how to write
// it instead.
func (r *Runner) AliasAddWords(name string, words []string, desc string, literal bool) error {
	if err := r.AliasAdd(name, strings.Join(words, " "), desc); err != nil {
		return err
	}
	if desc != "" || literal {
		return nil
	}
	at := -1
	for i, w := range words {
		if w == "--desc" {
			at = i
		}
	}
	if at < 0 {
		return nil
	}
	w := r.errOut()
	fmt.Fprintln(w, "uc: note: '--desc' is part of the alias command. To set a description, put it before the command:")
	if at == len(words)-2 {
		suggestion := append([]string{"uc", "alias", "add", name, "--desc", words[at+1]}, words[:at]...)
		quoted := make([]string, len(suggestion))
		for i, s := range suggestion {
			quoted[i] = shellQuote(s)
		}
		fmt.Fprintf(w, "          %s\n", strings.Join(quoted, " "))
	}
	return nil
}

// shellQuote returns s ready to paste into a shell: as-is when it has no
// special characters, otherwise in double quotes with \ " $ ` escaped.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$`!*?[]{}()<>|&;#~") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`")
	return `"` + r.Replace(s) + `"`
}

// warnIfAppendHazard notes on stderr when the executor's `"$@"` append
// would misbehave against this command text — a comment or a trailing
// control operator silently swallows or detaches the user's args
// (architecture §3.2b). A warning, not a rejection: the composition may
// still be what the user meant.
func (r *Runner) warnIfAppendHazard(command string) {
	if hazard := store.AppendHazard(command); hazard != "" {
		r.notef("%s — uc appends your invocation args as \"$@\" at the end", hazard)
	}
}

// warnIfShadowed prints a note if name won't actually resolve to the kind
// just registered — e.g. a new function shadowed by a same-named script,
// which always wins under the fixed script > alias > function precedence
// (architecture §3.2).
func (r *Runner) warnIfShadowed(name string, kind registry.Kind) {
	resolved, err := r.Reg.Resolve(name)
	if err == nil && resolved.Kind != kind {
		r.notef("%s is shadowed by a %s of the same name — `uc %s` will run the %s",
			name, resolved.Kind, name, resolved.Kind)
	}
}

// AliasEdit implements `uc alias edit <name>` — the same temp-file
// round-trip as FunctionEdit, editing the alias's command text.
// Desc/usage/examples are preserved as-is. The result is trimmed of
// surrounding whitespace: the executor appends `"$@"` to the command text
// (architecture §3.2b), and an editor's trailing newline would otherwise
// push that append onto its own line as a separate command. Saving an
// empty file changes nothing.
func (r *Runner) AliasEdit(name string) error {
	f, err := r.Reg.Store.Load()
	if err != nil {
		return err
	}
	a, ok := f.Aliases[name]
	if !ok {
		return fmt.Errorf("no alias named '%s'", name)
	}

	edited, err := r.editViaTempFile("uc-alias-*.sh", a.Command)
	if err != nil {
		return err
	}
	edited = strings.TrimSpace(edited)
	if edited == "" {
		fmt.Fprintln(r.Out, "aborted, nothing saved")
		return nil
	}
	a.Command = edited
	if err := r.Reg.Store.SetAlias(name, a); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "updated alias %s -> %s\n", name, names.EscapeControl(a.Command))
	r.warnIfAppendHazard(a.Command)
	return nil
}

// AliasRemove implements `uc alias remove <name>`.
func (r *Runner) AliasRemove(name string) error {
	if err := r.Reg.Store.RemoveAlias(name); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "removed alias %s\n", name)
	return nil
}

// AliasList implements `uc alias list` — a filtered view of `uc list`.
func (r *Runner) AliasList() error {
	f, err := r.Reg.Store.Load()
	if err != nil {
		return err
	}
	list := f.AliasNames()
	if len(list) == 0 {
		fmt.Fprintln(r.Out, "no aliases registered — add one with `uc alias add <name> <command...>`")
		return nil
	}
	r.printNameDesc(list, func(n string) string { return f.Aliases[n].Desc })
	return nil
}

// FunctionAdd implements `uc function add <name> <body> [--desc "..."]`.
func (r *Runner) FunctionAdd(name, body, desc string) error {
	if err := names.ValidName(name); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return errors.New("the function body cannot be empty")
	}
	if err := r.Reg.Store.SetFunction(name, store.Function{Body: body, Desc: desc}); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "added function %s\n", name)
	r.warnIfShadowed(name, registry.KindFunction)
	return nil
}

// FunctionRemove implements `uc function remove <name>`.
func (r *Runner) FunctionRemove(name string) error {
	if err := r.Reg.Store.RemoveFunction(name); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "removed function %s\n", name)
	return nil
}

// FunctionList implements `uc function list` — a filtered view of `uc list`.
func (r *Runner) FunctionList() error {
	f, err := r.Reg.Store.Load()
	if err != nil {
		return err
	}
	list := f.FunctionNames()
	if len(list) == 0 {
		fmt.Fprintln(r.Out, `no functions registered — add one with "uc function add <name> <body>"`)
		return nil
	}
	r.printNameDesc(list, func(n string) string { return f.Functions[n].Desc })
	return nil
}

func (r *Runner) printNameDesc(list []string, desc func(string) string) {
	width := 0
	for _, n := range list {
		width = max(width, len(n))
	}
	for _, n := range list {
		if d := names.CleanMeta(desc(n)); d != "" {
			fmt.Fprintf(r.Out, "%-*s  %s\n", width, n, d)
		} else {
			fmt.Fprintf(r.Out, "%-*s\n", width, n)
		}
	}
}

// FunctionEdit implements `uc function edit <name>` — a temp-file
// round-trip of the body through $EDITOR (architecture §3.2c). The
// editor's trailing newlines are dropped; saving an empty file changes
// nothing.
func (r *Runner) FunctionEdit(name string) error {
	f, err := r.Reg.Store.Load()
	if err != nil {
		return err
	}
	fn, ok := f.Functions[name]
	if !ok {
		return fmt.Errorf("no function named '%s'", name)
	}

	edited, err := r.editViaTempFile("uc-function-*.sh", fn.Body)
	if err != nil {
		return err
	}
	edited = strings.TrimRight(edited, "\r\n")
	if strings.TrimSpace(edited) == "" {
		fmt.Fprintln(r.Out, "aborted, nothing saved")
		return nil
	}
	fn.Body = edited
	if err := r.Reg.Store.SetFunction(name, fn); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "updated function %s\n", name)
	return nil
}
