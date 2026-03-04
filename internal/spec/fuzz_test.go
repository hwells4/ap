package spec

import "testing"

// FuzzParseSpec exercises ParseWithOptions with SkipStageLookup to avoid
// filesystem dependencies while still testing the full spec parser.
func FuzzParseSpec(f *testing.F) {
	// Valid stage names
	f.Add("ralph")
	f.Add("improve-plan")
	f.Add("refine-tasks")

	// Stage with iteration count
	f.Add("ralph:25")
	f.Add("ralph:1")
	f.Add("stage:999")

	// Chain expressions
	f.Add("improve-plan:5 -> refine-tasks:5")
	f.Add("alpha:2 -> beta:3")
	f.Add("a -> b -> c")
	f.Add("alpha:2 > beta:3")
	f.Add("alpha:2, beta:3")

	// File-style paths (will get file-not-found, but exercises detection)
	f.Add("./script.txt")
	f.Add("/tmp/prompt.md")
	f.Add("pipeline.yaml")
	f.Add("config.yml")

	// Malformed / edge cases
	f.Add("")
	f.Add("   ")
	f.Add("\t\n")
	f.Add("ralph:")
	f.Add(":10")
	f.Add("ralph:abc")
	f.Add("ralph:0")
	f.Add("ralph:-5")
	f.Add("ralph:1:2")
	f.Add(" -> refine-tasks:5")
	f.Add("improve-plan:5 -> ")
	f.Add("->")
	f.Add("a->b")
	f.Add("::::")
	f.Add("a:b:c:d")

	f.Fuzz(func(t *testing.T, input string) {
		// ParseWithOptions with SkipStageLookup avoids filesystem access
		// while exercising all parsing branches.
		_, _ = ParseWithOptions(input, ParseOptions{SkipStageLookup: true})
	})
}
