// Package fuzzy provides command/stage typo recovery helpers.
package fuzzy

import (
	"sort"
	"strings"
)

// Correction captures a normalization from user input to canonical value.
type Correction struct {
	From string
	To   string
	Hint string
}

var canonicalCommands = []string{
	"run",
	"list",
	"status",
	"resume",
	"kill",
	"logs",
	"clean",
}

var commandAliases = map[string]string{
	"abort":     "kill",
	"cancel":    "kill",
	"check":     "status",
	"continue":  "resume",
	"delete":    "clean",
	"exec":      "run",
	"execute":   "run",
	"follow":    "logs",
	"info":      "status",
	"launch":    "run",
	"ls":        "list",
	"pipelines": "list",
	"prune":     "clean",
	"remove":    "clean",
	"restart":   "resume",
	"rm":        "clean",
	"show":      "list",
	"start":     "run",
	"state":     "status",
	"stages":    "list",
	"stop":      "kill",
	"tail":      "logs",
	"terminate": "kill",
	"watch":     "logs",
}

var destructiveCommands = map[string]struct{}{
	"kill":  {},
	"clean": {},
}

var providerAliases = map[string]string{
	"anthropic": "claude",
}

// NormalizeCommand returns the canonical command and optional correction.
//
// Safety rule: destructive commands (kill/clean) are not typo-corrected via
// Levenshtein; only exact aliases can map to them.
func NormalizeCommand(input string) (string, *Correction, bool) {
	token := normalizeToken(input)
	if token == "" {
		return "", nil, false
	}

	if isCanonicalCommand(token) {
		return token, nil, true
	}

	if canonical, ok := commandAliases[token]; ok {
		correction := &Correction{
			From: token,
			To:   canonical,
			Hint: "command alias normalized",
		}
		return canonical, correction, true
	}

	best, dist, ok := nearestUnique(token, canonicalCommands)
	if !ok {
		return "", nil, false
	}
	if _, destructive := destructiveCommands[best]; destructive {
		return "", nil, false
	}
	if dist > allowedDistance(len(token)) {
		return "", nil, false
	}

	correction := &Correction{
		From: token,
		To:   best,
		Hint: "command typo corrected",
	}
	return best, correction, true
}

// SuggestCommands returns up to limit canonical command suggestions.
func SuggestCommands(input string, limit int) []string {
	if limit <= 0 {
		return []string{}
	}

	token := normalizeToken(input)
	if token == "" {
		return append([]string{}, canonicalCommands[:min(limit, len(canonicalCommands))]...)
	}

	type scored struct {
		name string
		dist int
	}
	scoredCommands := make([]scored, 0, len(canonicalCommands))
	for _, cmd := range canonicalCommands {
		scoredCommands = append(scoredCommands, scored{
			name: cmd,
			dist: Levenshtein(token, cmd),
		})
	}
	sort.SliceStable(scoredCommands, func(i, j int) bool {
		if scoredCommands[i].dist != scoredCommands[j].dist {
			return scoredCommands[i].dist < scoredCommands[j].dist
		}
		return scoredCommands[i].name < scoredCommands[j].name
	})

	maxDist := allowedDistance(len(token)) + 1
	out := make([]string, 0, limit)
	for _, entry := range scoredCommands {
		if entry.dist > maxDist && len(out) > 0 {
			break
		}
		out = append(out, entry.name)
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return append([]string{}, canonicalCommands[:min(limit, len(canonicalCommands))]...)
	}
	return out
}

// NormalizeProvider applies known provider aliases (for example anthropic->claude).
func NormalizeProvider(input string) (string, *Correction) {
	token := normalizeToken(input)
	if token == "" {
		return "", nil
	}

	if canonical, ok := providerAliases[token]; ok {
		return canonical, &Correction{
			From: token,
			To:   canonical,
			Hint: "provider alias normalized",
		}
	}
	return token, nil
}

// NormalizeStage returns the corrected stage name when a close match exists.
func NormalizeStage(input string, available []string) (string, *Correction, bool) {
	stageName := strings.TrimSpace(input)
	if stageName == "" || len(available) == 0 {
		return "", nil, false
	}

	for _, candidate := range available {
		if strings.EqualFold(stageName, candidate) {
			if candidate == stageName {
				return candidate, nil, true
			}
			return candidate, &Correction{
				From: stageName,
				To:   candidate,
				Hint: "stage case normalized",
			}, true
		}
	}

	best, dist, ok := nearestUnique(stageName, available)
	if !ok || dist > allowedDistance(len(stageName)) {
		return "", nil, false
	}

	return best, &Correction{
		From: stageName,
		To:   best,
		Hint: "stage name corrected",
	}, true
}

// SuggestStages returns up to limit likely stage names.
func SuggestStages(input string, available []string, limit int) []string {
	if limit <= 0 || len(available) == 0 {
		return []string{}
	}

	token := strings.TrimSpace(strings.ToLower(input))
	if token == "" {
		out := append([]string{}, available...)
		sort.Strings(out)
		return out[:min(limit, len(out))]
	}

	type scored struct {
		name string
		dist int
	}
	scoredStages := make([]scored, 0, len(available))
	for _, stage := range available {
		scoredStages = append(scoredStages, scored{
			name: stage,
			dist: Levenshtein(token, strings.ToLower(stage)),
		})
	}
	sort.SliceStable(scoredStages, func(i, j int) bool {
		if scoredStages[i].dist != scoredStages[j].dist {
			return scoredStages[i].dist < scoredStages[j].dist
		}
		return scoredStages[i].name < scoredStages[j].name
	})

	maxDist := allowedDistance(len(token)) + 1
	out := make([]string, 0, limit)
	for _, entry := range scoredStages {
		if entry.dist > maxDist && len(out) > 0 {
			break
		}
		out = append(out, entry.name)
		if len(out) == limit {
			break
		}
	}
	return out
}

// Levenshtein computes edit distance between two strings.
func Levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			curr[j] = min3(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func isCanonicalCommand(token string) bool {
	for _, cmd := range canonicalCommands {
		if token == cmd {
			return true
		}
	}
	return false
}

func nearestUnique(input string, options []string) (string, int, bool) {
	if len(options) == 0 {
		return "", 0, false
	}

	normalizedInput := strings.ToLower(strings.TrimSpace(input))
	bestName := ""
	bestDist := -1
	tied := false

	for _, option := range options {
		dist := Levenshtein(normalizedInput, strings.ToLower(option))
		if bestDist == -1 || dist < bestDist {
			bestDist = dist
			bestName = option
			tied = false
			continue
		}
		if dist == bestDist && !strings.EqualFold(option, bestName) {
			tied = true
		}
	}

	if bestDist == -1 || tied {
		return "", 0, false
	}
	return bestName, bestDist, true
}

func allowedDistance(length int) int {
	if length <= 4 {
		return 1
	}
	return 2
}

func normalizeToken(input string) string {
	return strings.ToLower(strings.TrimSpace(input))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func min3(a, b, c int) int {
	return min(min(a, b), c)
}
