// Package spec parses run specification strings into typed AST nodes.
//
// Precedence (from AGENTS.md):
//
//  1. Contains "->" → ChainSpec (M3, rejected for now)
//  2. Ends .yaml/.yml → FileSpec(yaml)
//  3. Ends .md or starts "./"/"/" → FileSpec(prompt)
//  4. Contains ":" → StageSpec(name, count)
//  5. Otherwise → StageSpec(name)
package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/hwells4/ap/internal/fsutil"
)

var (
	// ErrEmpty is returned when the spec input is empty.
	ErrEmpty = errors.New("spec: empty input")
	// ErrFileNotFound is returned when a file spec references a missing file.
	ErrFileNotFound = errors.New("spec: file not found")
	// ErrInvalidSpec is returned for malformed spec syntax.
	ErrInvalidSpec = errors.New("spec: invalid specification")
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
}

// Kind returns KindStage.
func (s StageSpec) Kind() SpecKind { return KindStage }

// Raw returns the original input string.
func (s StageSpec) Raw() string { return s.raw }

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

// Parse parses a run specification string into a typed Spec.
//
// It follows the documented precedence order without fallthrough:
// file paths never fall through to stage lookup on FILE_NOT_FOUND.
func Parse(input string) (Spec, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmpty
	}

	// 1. Chain: contains "->"
	if strings.Contains(input, "->") {
		return nil, fmt.Errorf("%w: chain expressions (\"->\" syntax) are not yet supported", ErrInvalidSpec)
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
		return parseStageSpec(input)
	}

	// 5. Bare stage name
	return StageSpec{raw: input, Name: input}, nil
}

func parseFileSpec(input string, kind SpecKind) (FileSpec, error) {
	if !fsutil.FileExists(input) {
		return FileSpec{}, fmt.Errorf("%w: %s", ErrFileNotFound, input)
	}
	return FileSpec{raw: input, Path: input, FileKind: kind}, nil
}

func parseStageSpec(input string) (StageSpec, error) {
	idx := strings.LastIndex(input, ":")
	name := input[:idx]
	countStr := input[idx+1:]

	if name == "" {
		return StageSpec{}, fmt.Errorf("%w: stage name is empty in %q", ErrInvalidSpec, input)
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
