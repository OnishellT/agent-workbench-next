// SPDX-License-Identifier: AGPL-3.0-only

// Package foundrybundle embeds the vendored Conversation Foundry runtime (the Python
// package foundry_loop with its prebuilt offline UI) so the single Workbench binary can
// unpack and run it. See WORKBENCH.md for the pin and the local patches; rebuild the UI
// (make foundry-ui) before compiling Go when ui/ changes.
package foundrybundle

import (
	"embed"
	"io/fs"
)

// all: keeps __init__.py and __main__.py, which a plain pattern would drop. examples/ is
// read by the package itself (the guided HTTP example, foundry_loop/app.py), so it ships
// beside it.
//
//go:embed all:foundry_loop all:examples
var runtime embed.FS

// Runtime is the tree to extract and put on PYTHONPATH: foundry_loop/ and examples/,
// laid out as upstream lays them out (the package finds examples/ as its sibling).
func Runtime() fs.FS { return runtime }
