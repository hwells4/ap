package stages

import "embed"

// FS contains built-in stage assets shipped in the ap binary.
//
//go:embed */stage.yaml */prompt.md */prompts/*.md */fixtures/*
var FS embed.FS
