// Package all registers every built-in atelier tool into the plugin
// registry. It is the single wiring point between the tool packages and
// the core dispatcher: the atelier binary blank-imports this package so
// that, at init time, every tool's manifest + command tree becomes
// available to `atelier tools`, `atelier init`, the tool selector, and
// `atelier doctor`.
//
// Adding a built-in tool = add one line here (and a package under
// internal/tools/<name> exposing Manifest + AddCommands). There is no
// PATH scan and no subprocess manifest protocol — the core knows its
// built-in tools at compile time.
package all

import (
	"github.com/vyrwu/atelier/internal/plugin"

	"github.com/vyrwu/atelier/internal/tools/aws"
	"github.com/vyrwu/atelier/internal/tools/k8s"
	"github.com/vyrwu/atelier/internal/tools/pg"
	"github.com/vyrwu/atelier/internal/tools/toolselector"
	"github.com/vyrwu/atelier/internal/tools/workspaces"
)

func init() {
	plugin.RegisterBuiltin(aws.Manifest, aws.AddCommands)
	plugin.RegisterBuiltin(k8s.Manifest, k8s.AddCommands)
	plugin.RegisterBuiltin(pg.Manifest, pg.AddCommands)
	plugin.RegisterBuiltin(toolselector.Manifest, toolselector.AddCommands)
	plugin.RegisterBuiltin(workspaces.Manifest, workspaces.AddCommands)
}
