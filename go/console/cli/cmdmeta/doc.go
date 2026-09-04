// Package cmdmeta reads the kit/ command annotations off a cobra
// command. It is deliberately a LEAF package: it imports nothing
// from hop.top/kit beyond the standard library and cobra.
//
// # Why this package exists
//
// Reflection ([hop.top/kit/go/ai/cmdreflect]) needs these readers,
// and transports need reflection. Before this package the readers
// lived in [hop.top/kit/go/console/cli], which imports
// [hop.top/kit/go/transport/api] — so any transport that reflected
// the command tree closed a cycle:
//
//	transport/api → ai/cmdreflect → console/cli → transport/api
//
// That cycle is why an HTTP surface could not reflect the tree it
// was serving. Splitting the pure annotation readers out of cli
// breaks the middle edge: cmdreflect depends on cmdmeta, cmdmeta
// depends on nothing of kit's, and a transport is free to reflect.
//
// # Relationship to cli
//
// The setters (SetTopLevelVerb, SetOutputSchema, SetExamples, …)
// stay in cli, because they are the adopter-facing declaration API
// and several validate against cli-owned state. Only the readers
// move. Every name here is still reachable under its original
// cli.* spelling, which forwards; adopters need change nothing.
package cmdmeta
