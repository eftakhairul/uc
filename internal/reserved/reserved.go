// Package reserved lists the subcommand names uc reserves for itself.
// Split out as its own tiny package (no other internal deps) so both
// commands and completion can depend on it without an import cycle.
package reserved

import "slices"

// Names are the subcommands that always win over a same-named script
// (architecture §3.1, §6 decision 1). "__complete" is hidden — it's
// reserved but not shown in `uc help`.
var Names = []string{
	"list", "ls",
	"add",
	"remove", "rm",
	"which",
	"edit",
	"completion",
	"history",
	"alias",
	"function",
	"help",
	"version",
	"__complete",
}

// Hidden are reserved names not shown in `uc help`.
var Hidden = map[string]bool{
	"__complete": true,
}

func Is(name string) bool {
	return slices.Contains(Names, name)
}
