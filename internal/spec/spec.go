// Package spec parses run specification strings into typed AST nodes.
//
// Precedence (from AGENTS.md):
//
//  1. Contains chain separators ("->", and recovered ">" / ",") → ChainSpec
//  2. Ends .yaml/.yml → FileSpec(yaml)
//  3. Ends .md or starts "./"/"/" → FileSpec(prompt)
//  4. Contains ":" → StageSpec(name, count)
//  5. Otherwise → StageSpec(name)
package spec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/hwells4/ap/internal/compile"
	"github.com/hwells4/ap/internal/fsutil"
	"github.com/hwells4/ap/internal/stage"
)

var (
	// ErrEmpty is returned when the spec input is empty.
	ErrEmpty = errors.New("spec: empty input")
	// ErrFileNotFound is returned when a file spec references a missing file.
	ErrFileNotFound = errors.New("spec: file not found")
	// ErrInvalidSpec is returned for malformed spec syntax.
	ErrInvalidSpec = errors.New("spec: invalid specification")
	// ErrStageNotFound is returned when a stage spec cannot be resolved.
	ErrStageNotFound = errors.New("spec: stage not found")
)

// SpecKind identifies the variant of a parsed specification.
type SpecKind int

const (
	KindStage      SpecKind = iota // Stage name, optionally with :N count
	KindFilePrompt                 // Prompt file (.md or path starting with ./ or /)
	KindFileYAML                   // Pipeline file (.yaml/.yml)
	KindChain                      // Chain expression with -> (M3+, not yet supported)
)

// Spec is the common interface for all parsed spec variants.
type Spec interface {
	Kind() SpecKind
	Raw() string
}

// StageSpec names a stage with an optional iteration count override.
type StageSpec struct {
	raw        string
	Name       string // Stage name (e.g., "ralph")
	Iterations int    // 0 = use stage default
	Definition stage.Definition
}

// Kind returns KindStage.
func (s StageSpec) Kind() SpecKind { return KindStage }

// Raw returns the original input string.
func (s StageSpec) Raw() string { return s.raw }

// ChainSpec represents a parsed chain expression.
type ChainSpec struct {
	raw    string
	Stages []StageSpec
}

// Kind returns KindChain.
func (c ChainSpec) Kind() SpecKind { return KindChain }

// Raw returns the original input string.
func (c ChainSpec) Raw() string { return c.raw }

// Repeat expands the chain by repeating its stages n times.
func (c ChainSpec) Repeat(n int) ChainSpec {
	if n <= 1 {
		return c
	}
	repeated := make([]StageSpec, 0, len(c.Stages)*n)
	for i := 0; i < n; i++ {
		repeated = append(repeated, c.Stages...)
	}
	return ChainSpec{
		raw:    fmt.Sprintf("(%s) x%d", c.raw, n),
		Stages: repeated,
	}
}

// ToPipeline converts a chain into a sequential Pipeline representation.
//
// Each stage is converted into one node. For all nodes after the first,
// inputs.from is set to the previous node id with select=latest.
func (c ChainSpec) ToPipeline() compile.Pipeline {
	usedIDs := map[string]int{}
	nodes := make([]compile.Node, 0, len(c.Stages))

	var prevID string
	for i, stageSpec := range c.Stages {
		baseID := strings.TrimSpace(stageSpec.Name)
		if baseID == "" {
			baseID = fmt.Sprintf("stage-%d", i+1)
		}
		nodeID := uniqueNodeID(baseID, usedIDs)

		node := compile.Node{
			ID:    nodeID,
			Stage: stageSpec.Name,
			Runs:  stageSpec.Iterations,
		}
		if i > 0 {
			node.Inputs = compile.Inputs{
				From:   prevID,
				Select: compile.SelectLatest,
			}
		}

		nodes = append(nodes, node)
		prevID = nodeID
	}

	return compile.Pipeline{
		Name:  "chain",
		Nodes: nodes,
	}
}

// FileSpec references a file as a prompt or pipeline definition.
type FileSpec struct {
	raw      string
	Path     string   // Resolved file path
	FileKind SpecKind // KindFilePrompt or KindFileYAML
}

// Kind returns KindFilePrompt or KindFileYAML.
func (f FileSpec) Kind() SpecKind { return f.FileKind }

// Raw returns the original input string.
func (f FileSpec) Raw() string { return f.raw }

// ParseOptions controls stage-resolution behavior during parsing.
type ParseOptions struct {
	SkipStageLookup  bool
	StageResolveOpts stage.ResolveOptions
}

// Parse parses a run specification string into a typed Spec.
//
// It follows the documented precedence order without fallthrough:
// file paths never fall through to stage lookup on FILE_NOT_FOUND.
func Parse(input string) (Spec, error) {
	return ParseWithOptions(input, defaultParseOptions())
}

// ParseWithOptions parses a run specification and optionally resolves stages.
func ParseWithOptions(input string, opts ParseOptions) (Spec, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmpty
	}

	// 0. Repeat wrapper: (chain) xN
	if inner, count, ok := parseRepeatWrapper(input); ok {
		if count <= 0 {
			return nil, fmt.Errorf("%w: repeat count must be positive, got %d", ErrInvalidSpec, count)
		}
		innerSpec, err := ParseWithOptions(inner, opts)
		if err != nil {
			return nil, err
		}
		switch s := innerSpec.(type) {
		case ChainSpec:
			return s.Repeat(count), nil
		case StageSpec:
			chain := ChainSpec{raw: inner, Stages: []StageSpec{s}}
			return chain.Repeat(count), nil
		default:
			return nil, fmt.Errorf("%w: repeat syntax only works with stage or chain specs", ErrInvalidSpec)
		}
	}

	chainInput, isChain := normalizeChainInput(input)
	// 1. Chain: contains "->" or recovered separators.
	if isChain {
		return parseChainSpec(input, chainInput, opts)
	}

	// 2. YAML file: ends with .yaml or .yml
	ext := strings.ToLower(filepath.Ext(input))
	if ext == ".yaml" || ext == ".yml" {
		return parseFileSpec(input, KindFileYAML)
	}

	// 3. Prompt file: ends with .md OR starts with ./ or /
	if ext == ".md" || strings.HasPrefix(input, "./") || strings.HasPrefix(input, "/") {
		return parseFileSpec(input, KindFilePrompt)
	}

	// 4. Stage with count: contains ":"
	if strings.Contains(input, ":") {
		stageSpec, err := parseStageSpec(input)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveStageSpec(stageSpec, opts)
		if err != nil {
			return nil, err
		}
		return resolved, nil
	}

	// 4b. Recover space-separated iteration shorthand: "ralph 25" → "ralph:25"
	if spaceIdx := strings.LastIndex(input, " "); spaceIdx > 0 {
		namePart := strings.TrimSpace(input[:spaceIdx])
		countPart := strings.TrimSpace(input[spaceIdx+1:])
		if namePart != "" && countPart != "" {
			if count, err := strconv.Atoi(countPart); err == nil && count > 0 {
				recovered := fmt.Sprintf("%s:%d", namePart, count)
				return parseStageSpecAndResolve(input, recovered, namePart, count, opts)
			}
		}
	}

	// 5. Bare stage name
	stageSpec, err := resolveStageSpec(StageSpec{raw: input, Name: input}, opts)
	if err != nil {
		return nil, err
	}
	return stageSpec, nil
}

func defaultParseOptions() ParseOptions {
	opts := ParseOptions{}
	if cwd, err := os.Getwd(); err == nil {
		opts.StageResolveOpts.ProjectRoot = cwd
	}
	return opts
}

func parseFileSpec(input string, kind SpecKind) (FileSpec, error) {
	if !fsutil.FileExists(input) {
		return FileSpec{}, fmt.Errorf("%w: %s", ErrFileNotFound, input)
	}
	return FileSpec{raw: input, Path: input, FileKind: kind}, nil
}

func parseStageSpec(input string) (StageSpec, error) {
	if strings.Count(input, ":") != 1 {
		return StageSpec{}, fmt.Errorf(
			"%w: invalid stage iteration syntax %q; expected format <stage>:<positive-integer>",
			ErrInvalidSpec,
			input,
		)
	}

	idx := strings.LastIndex(input, ":")
	name := strings.TrimSpace(input[:idx])
	countStr := strings.TrimSpace(input[idx+1:])

	if name == "" {
		return StageSpec{}, fmt.Errorf("%w: stage name is empty in %q", ErrInvalidSpec, input)
	}
	if countStr == "" {
		return StageSpec{}, fmt.Errorf("%w: iteration count is empty in %q", ErrInvalidSpec, input)
	}

	count, err := strconv.Atoi(countStr)
	if err != nil {
		return StageSpec{}, fmt.Errorf(
			"%w: invalid iteration count %q in %q; expected a positive integer (e.g., %s:10)",
			ErrInvalidSpec, countStr, input, name,
		)
	}
	if count <= 0 {
		return StageSpec{}, fmt.Errorf(
			"%w: iteration count must be positive, got %d in %q",
			ErrInvalidSpec, count, input,
		)
	}

	return StageSpec{
		raw:        input,
		Name:       name,
		Iterations: count,
	}, nil
}

func parseStageSpecAndResolve(rawInput, recovered, name string, count int, opts ParseOptions) (StageSpec, error) {
	ss := StageSpec{raw: recovered, Name: name, Iterations: count}
	return resolveStageSpec(ss, opts)
}

func resolveStageSpec(stageSpec StageSpec, opts ParseOptions) (StageSpec, error) {
	if opts.SkipStageLookup {
		return stageSpec, nil
	}

	definition, err := stage.ResolveStage(stageSpec.Name, opts.StageResolveOpts)
	if err != nil {
		return StageSpec{}, fmt.Errorf("%w: %q: %v", ErrStageNotFound, stageSpec.Name, err)
	}

	stageSpec.Definition = definition
	return stageSpec, nil
}

var (
	chainArrowRecoveryPattern = regexp.MustCompile(`\s>\s`)
	chainCommaRecoveryPattern = regexp.MustCompile(`\s*,\s*`)
	repeatPattern             = regexp.MustCompile(`^\((.+)\)\s*[xX*](\d+)$`)
)

func parseRepeatWrapper(input string) (inner string, count int, ok bool) {
	m := repeatPattern.FindStringSubmatch(input)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(m[1]), n, true
}

func normalizeChainInput(input string) (string, bool) {
	if strings.Contains(input, "->") {
		return input, true
	}

	if chainArrowRecoveryPattern.MatchString(input) {
		recovered := chainArrowRecoveryPattern.ReplaceAllString(input, " -> ")
		return recovered, strings.Contains(recovered, "->")
	}

	if chainCommaRecoveryPattern.MatchString(input) {
		recovered := chainCommaRecoveryPattern.ReplaceAllString(input, " -> ")
		return recovered, strings.Contains(recovered, "->")
	}

	return input, false
}

func parseChainSpec(rawInput, chainInput string, opts ParseOptions) (ChainSpec, error) {
	segments := strings.Split(chainInput, "->")
	if len(segments) < 2 {
		return ChainSpec{}, fmt.Errorf("%w: invalid chain: expected at least two stages", ErrInvalidSpec)
	}

	stages := make([]StageSpec, 0, len(segments))
	for idx, segment := range segments {
		stageText := strings.TrimSpace(segment)
		if stageText == "" {
			if idx == 0 {
				return ChainSpec{}, fmt.Errorf("%w: invalid chain: expected stage name before ->", ErrInvalidSpec)
			}
			return ChainSpec{}, fmt.Errorf("%w: invalid chain: expected stage name after ->", ErrInvalidSpec)
		}

		var stageSpec StageSpec
		var err error
		if strings.Contains(stageText, ":") {
			stageSpec, err = parseStageSpec(stageText)
			if err != nil {
				return ChainSpec{}, err
			}
		} else {
			stageSpec = StageSpec{raw: stageText, Name: stageText}
		}

		stageSpec, err = resolveStageSpec(stageSpec, opts)
		if err != nil {
			return ChainSpec{}, err
		}
		stages = append(stages, stageSpec)
	}

	return ChainSpec{
		raw:    rawInput,
		Stages: stages,
	}, nil
}

func uniqueNodeID(base string, used map[string]int) string {
	count := used[base]
	if count == 0 {
		used[base] = 1
		return base
	}

	count++
	used[base] = count
	return fmt.Sprintf("%s-%d", base, count)
}
