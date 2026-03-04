package fuzzy

import "testing"

// FuzzLevenshtein exercises the Levenshtein distance function.
func FuzzLevenshtein(f *testing.F) {
	f.Add("run", "run")
	f.Add("run", "rum")
	f.Add("", "abc")
	f.Add("abc", "")
	f.Add("", "")
	f.Add("kitten", "sitting")
	f.Add("saturday", "sunday")
	f.Add("ab", "ba")

	f.Fuzz(func(t *testing.T, a, b string) {
		dist := Levenshtein(a, b)
		if dist < 0 {
			t.Fatalf("Levenshtein(%q, %q) = %d, want >= 0", a, b, dist)
		}
		// Identity: same strings have distance 0
		if a == b && dist != 0 {
			t.Fatalf("Levenshtein(%q, %q) = %d, want 0", a, b, dist)
		}
		// Symmetry: distance(a,b) == distance(b,a)
		reverse := Levenshtein(b, a)
		if dist != reverse {
			t.Fatalf("Levenshtein(%q, %q) = %d but Levenshtein(%q, %q) = %d", a, b, dist, b, a, reverse)
		}
	})
}

// FuzzNormalizeCommand exercises command normalization with arbitrary input.
func FuzzNormalizeCommand(f *testing.F) {
	f.Add("run")
	f.Add("list")
	f.Add("start")
	f.Add("stop")
	f.Add("kil")
	f.Add("listt")
	f.Add("")
	f.Add("   ")
	f.Add("RESUME")
	f.Add("xyzzy")
	f.Add("anthropic")

	f.Fuzz(func(t *testing.T, input string) {
		_, _, _ = NormalizeCommand(input)
	})
}

// FuzzNormalizeStage exercises stage normalization with arbitrary input.
func FuzzNormalizeStage(f *testing.F) {
	f.Add("ralph", "ralph,review,elegance")
	f.Add("ralhp", "ralph,review")
	f.Add("", "ralph")
	f.Add("xyz", "")
	f.Add("improve-plan", "improve-plan,refine-tasks")
	f.Add("RALPH", "ralph,review")

	f.Fuzz(func(t *testing.T, input, availableCSV string) {
		// Split the CSV into available stages
		var available []string
		if availableCSV != "" {
			start := 0
			for i := 0; i <= len(availableCSV); i++ {
				if i == len(availableCSV) || availableCSV[i] == ',' {
					if start < i {
						available = append(available, availableCSV[start:i])
					}
					start = i + 1
				}
			}
		}
		_, _, _ = NormalizeStage(input, available)
	})
}
