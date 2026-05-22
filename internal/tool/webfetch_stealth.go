package tool

import (
	_ "embed"
)

// stealthScript is the medium stealth patch set. Embedded so it's
// trivially auditable and version-controlled alongside the rest of
// the webfetch code. Runs via Page.addScriptToEvaluateOnNewDocument
// before any page script on every frame.
//
//go:embed webfetch_stealth.js
var stealthScript string
