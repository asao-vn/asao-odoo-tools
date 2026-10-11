package clicore

// DefaultProgram is the program name every command tree reports unless its ROOT says otherwise.
//
// It exists because clicore is now shared by two DIFFERENT executables with two DIFFERENT trust
// models — the governed `asao` control plane and the standalone `aerp` Odoo tool (P5) — and a
// caller must never be told to run `asao ...` by a binary that is not asao. Every user-facing
// rendering of a command path (help text, the JSON envelope's `command`, discovery documents, and
// every "run X to see accepted flags" remediation) goes through programOf so the two can never
// disagree about which executable is speaking.
//
// It is deliberately NOT a package-level variable a binary mutates at init: a mutable global would
// make the program name a property of the PROCESS, and the privileged authority runtime hosts the
// governed tree inside a process that must keep reporting `asao` regardless of anything else linked
// into it. Making it a field of the ROOT Command makes it a property of the TREE, which is what it
// actually is.
const DefaultProgram = "asao"

// programOf reports the executable name for the tree that `command` belongs to.
//
// It walks to the root because Program is meaningful only there — a subtree cannot rename the
// binary it is mounted in. link() must already have run for the walk to terminate anywhere but at
// `command` itself; Parse and Prepare both call it, and a tree rendered before either would simply
// report the default, never a wrong name.
func programOf(command *Command) string {
	if command == nil {
		return DefaultProgram
	}
	for command.parent != nil {
		command = command.parent
	}
	if command.Program == "" {
		return DefaultProgram
	}
	return command.Program
}

// qualify renders a command path the way a caller of THIS tree's executable types it.
func qualify(command *Command) string {
	return joinProgram(programOf(command), command.PathString())
}

// joinProgram is the one place "<program> <path>" is spelled, so a trailing space can never appear
// on the root command's own rendering.
func joinProgram(program, path string) string {
	if path == "" {
		return program
	}
	return program + " " + path
}
