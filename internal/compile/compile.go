// Package compile compiles pipeline YAML files into typed pipeline objects.
package compile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hwells4/ap/internal/stage"
	"gopkg.in/yaml.v3"
)

const (
	// SelectLatest keeps only the latest output from the source node.
	SelectLatest = "latest"
	// SelectAll keeps all historical outputs from the source node.
	SelectAll = "all"
)

var (
	// ErrInvalidPipeline indicates malformed pipeline YAML.
	ErrInvalidPipeline = errors.New("compile: invalid pipeline")
)

// Pipeline is the compiled representation of a pipeline YAML file.
type Pipeline struct {
	Name        string            `yaml:"name" json:"name"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	Commands    map[string]string `yaml:"commands,omitempty" json:"commands,omitempty"`
	Nodes       []Node            `yaml:"nodes" json:"nodes"`
	SourcePath  string            `yaml:"-" json:"source_path"`
}

// Node is one executable unit in a pipeline.
type Node struct {
	ID          string             `yaml:"id" json:"id"`
	Stage       string             `yaml:"stage,omitempty" json:"stage,omitempty"`
	Runs        int                `yaml:"runs,omitempty" json:"runs,omitempty"`
	Provider    string             `yaml:"provider,omitempty" json:"provider,omitempty"`
	Model       string             `yaml:"model,omitempty" json:"model,omitempty"`
	Context     string             `yaml:"context,omitempty" json:"context,omitempty"`
	Inputs      Inputs             `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Termination Termination        `yaml:"termination,omitempty" json:"termination,omitempty"`
	Swarm       *SwarmBlock         `yaml:"swarm,omitempty" json:"swarm,omitempty"`
	Meta        map[string]any     `yaml:",inline" json:"meta,omitempty"`
}

// Inputs configures stage-to-stage data wiring.
type Inputs struct {
	From         string      `yaml:"from,omitempty" json:"from,omitempty"`
	Select       string      `yaml:"select,omitempty" json:"select,omitempty"`
	FromInitial  bool        `yaml:"from_initial,omitempty" json:"from_initial,omitempty"`
	FromSwarm    interface{} `yaml:"from_swarm,omitempty" json:"from_swarm,omitempty"`
}

// Termination holds per-node termination overrides.
type Termination struct {
	Type          string `yaml:"type,omitempty" json:"type,omitempty"`
	Iterations    int    `yaml:"iterations,omitempty" json:"iterations,omitempty"`
	Consensus     int    `yaml:"consensus,omitempty" json:"consensus,omitempty"`
	Max           int    `yaml:"max,omitempty" json:"max,omitempty"`
	MinIterations int    `yaml:"min_iterations,omitempty" json:"min_iterations,omitempty"`
	Agents        int    `yaml:"agents,omitempty" json:"agents,omitempty"`
	Accept        string `yaml:"accept,omitempty" json:"accept,omitempty"`
}

// SwarmBlock describes a swarm provider execution block.
type SwarmBlock struct {
	Providers ProviderList  `yaml:"providers" json:"providers"`
	Stages    []SwarmStage  `yaml:"stages" json:"stages"`
}

// ProviderList supports scalar and object provider declarations.
type ProviderList []ProviderConfig

// ProviderConfig describes one provider in a swarm block.
type ProviderConfig struct {
	Name   string `yaml:"name" json:"name"`
	Model  string `yaml:"model,omitempty" json:"model,omitempty"`
	Inputs Inputs `yaml:"inputs,omitempty" json:"inputs,omitempty"`
}

// SwarmStage is a stage definition nested under swarm.stages.
type SwarmStage struct {
	ID          string      `yaml:"id,omitempty" json:"id,omitempty"`
	Name        string      `yaml:"name,omitempty" json:"name,omitempty"`
	Stage       string      `yaml:"stage" json:"stage"`
	Runs        int         `yaml:"runs,omitempty" json:"runs,omitempty"`
	Context     string      `yaml:"context,omitempty" json:"context,omitempty"`
	Termination Termination `yaml:"termination,omitempty" json:"termination,omitempty"`
}

type pipelineDocument struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Commands    map[string]string `yaml:"commands,omitempty"`
	Nodes       []Node            `yaml:"nodes"`
	Stages      []Node            `yaml:"stages"`
}

// Compile parses and validates a YAML pipeline file.
func Compile(yamlPath string) (*Pipeline, error) {
	path := strings.TrimSpace(yamlPath)
	if path == "" {
		return nil, fmt.Errorf("%w: yaml path is empty", ErrInvalidPipeline)
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("%w: resolve path %q: %v", ErrInvalidPipeline, yamlPath, err)
		}
		path = abs
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %q: %v", ErrInvalidPipeline, path, err)
	}

	var doc pipelineDocument
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: parse %q: %v", ErrInvalidPipeline, path, err)
	}

	nodes, err := effectiveNodes(doc)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(doc.Name) == "" {
		return nil, fmt.Errorf("%w: pipeline name is required", ErrInvalidPipeline)
	}

	pipeline := &Pipeline{
		Name:        strings.TrimSpace(doc.Name),
		Description: strings.TrimSpace(doc.Description),
		Commands:    doc.Commands,
		Nodes:       nodes,
		SourcePath:  path,
	}
	if pipeline.Commands == nil {
		pipeline.Commands = map[string]string{}
	}

	if err := validatePipeline(pipeline, path); err != nil {
		return nil, err
	}
	return pipeline, nil
}

func effectiveNodes(doc pipelineDocument) ([]Node, error) {
	if len(doc.Nodes) > 0 && len(doc.Stages) > 0 {
		return nil, fmt.Errorf("%w: both nodes and deprecated stages are set; use nodes only", ErrInvalidPipeline)
	}
	switch {
	case len(doc.Nodes) > 0:
		return doc.Nodes, nil
	case len(doc.Stages) > 0:
		return doc.Stages, nil
	default:
		return nil, fmt.Errorf("%w: pipeline must define nodes (or deprecated stages)", ErrInvalidPipeline)
	}
}

func validatePipeline(pipeline *Pipeline, sourcePath string) error {
	nodeIDs := make(map[string]int, len(pipeline.Nodes))
	edges := make(map[string][]string, len(pipeline.Nodes))

	projectRoot := detectProjectRoot(filepath.Dir(sourcePath))
	resolveOpts := stage.ResolveOptions{
		ProjectRoot: projectRoot,
		PipelineDir: projectRoot,
	}

	for idx := range pipeline.Nodes {
		node := &pipeline.Nodes[idx]

		node.ID = strings.TrimSpace(node.ID)
		node.Stage = strings.TrimSpace(node.Stage)
		node.Provider = strings.TrimSpace(node.Provider)
		node.Model = strings.TrimSpace(node.Model)
		node.Context = strings.TrimSpace(node.Context)
		node.Inputs.From = strings.TrimSpace(node.Inputs.From)
		node.Inputs.Select = strings.TrimSpace(node.Inputs.Select)
		node.Termination.Type = strings.TrimSpace(node.Termination.Type)

		if node.ID == "" {
			return fmt.Errorf("%w: node[%d] id is required", ErrInvalidPipeline, idx)
		}
		if _, exists := nodeIDs[node.ID]; exists {
			return fmt.Errorf("%w: duplicate node id %q", ErrInvalidPipeline, node.ID)
		}
		nodeIDs[node.ID] = idx

		if node.Inputs.Select == "" {
			node.Inputs.Select = SelectLatest
		}
		if node.Inputs.Select != SelectLatest && node.Inputs.Select != SelectAll {
			return fmt.Errorf(
				"%w: node %q inputs.select must be one of %q or %q, got %q",
				ErrInvalidPipeline,
				node.ID,
				SelectLatest,
				SelectAll,
				node.Inputs.Select,
			)
		}

		if node.Swarm != nil {
			if err := validateParallelNode(node, resolveOpts); err != nil {
				return err
			}
		} else {
			if node.Stage == "" {
				return fmt.Errorf("%w: node %q must set stage or swarm", ErrInvalidPipeline, node.ID)
			}
			if _, err := stage.ResolveStage(node.Stage, resolveOpts); err != nil {
				return fmt.Errorf("%w: node %q references unknown stage %q: %v", ErrInvalidPipeline, node.ID, node.Stage, err)
			}
		}

		if node.Inputs.From != "" {
			edges[node.ID] = append(edges[node.ID], node.Inputs.From)
		}
	}

	for _, node := range pipeline.Nodes {
		if node.Inputs.From != "" {
			if _, ok := nodeIDs[node.Inputs.From]; !ok {
				return fmt.Errorf(
					"%w: node %q inputs.from %q does not match any node id",
					ErrInvalidPipeline,
					node.ID,
					node.Inputs.From,
				)
			}
		}
	}

	if cycle := findCycle(edges); len(cycle) > 0 {
		return fmt.Errorf("%w: circular dependency detected: %s", ErrInvalidPipeline, strings.Join(cycle, " -> "))
	}
	return nil
}

func validateParallelNode(node *Node, resolveOpts stage.ResolveOptions) error {
	if node.Swarm == nil {
		return nil
	}
	block := node.Swarm
	if len(block.Providers) == 0 {
		return fmt.Errorf("%w: node %q swarm.providers must not be empty", ErrInvalidPipeline, node.ID)
	}
	if len(block.Stages) == 0 {
		return fmt.Errorf("%w: node %q swarm.stages must not be empty", ErrInvalidPipeline, node.ID)
	}

	for i := range block.Providers {
		block.Providers[i].Name = strings.TrimSpace(block.Providers[i].Name)
		block.Providers[i].Model = strings.TrimSpace(block.Providers[i].Model)
		block.Providers[i].Inputs.From = strings.TrimSpace(block.Providers[i].Inputs.From)
		block.Providers[i].Inputs.Select = strings.TrimSpace(block.Providers[i].Inputs.Select)
		if block.Providers[i].Name == "" {
			return fmt.Errorf("%w: node %q swarm.providers[%d] has empty name", ErrInvalidPipeline, node.ID, i)
		}
		if block.Providers[i].Inputs.Select == "" && block.Providers[i].Inputs.From != "" {
			block.Providers[i].Inputs.Select = SelectLatest
		}
		if block.Providers[i].Inputs.Select != "" &&
			block.Providers[i].Inputs.Select != SelectLatest &&
			block.Providers[i].Inputs.Select != SelectAll {
			return fmt.Errorf(
				"%w: node %q swarm.providers[%d] inputs.select must be %q or %q",
				ErrInvalidPipeline,
				node.ID,
				i,
				SelectLatest,
				SelectAll,
			)
		}
	}

	stageIDs := map[string]bool{}
	for i := range block.Stages {
		block.Stages[i].ID = strings.TrimSpace(block.Stages[i].ID)
		block.Stages[i].Name = strings.TrimSpace(block.Stages[i].Name)
		block.Stages[i].Stage = strings.TrimSpace(block.Stages[i].Stage)
		block.Stages[i].Context = strings.TrimSpace(block.Stages[i].Context)
		block.Stages[i].Termination.Type = strings.TrimSpace(block.Stages[i].Termination.Type)

		if block.Stages[i].ID == "" && block.Stages[i].Name != "" {
			block.Stages[i].ID = block.Stages[i].Name
		}
		if block.Stages[i].ID != "" {
			if stageIDs[block.Stages[i].ID] {
				return fmt.Errorf(
					"%w: node %q swarm.stages has duplicate id/name %q",
					ErrInvalidPipeline,
					node.ID,
					block.Stages[i].ID,
				)
			}
			stageIDs[block.Stages[i].ID] = true
		}
		if block.Stages[i].Stage == "" {
			return fmt.Errorf("%w: node %q swarm.stages[%d] stage is required", ErrInvalidPipeline, node.ID, i)
		}
		if _, err := stage.ResolveStage(block.Stages[i].Stage, resolveOpts); err != nil {
			return fmt.Errorf(
				"%w: node %q swarm.stages[%d] references unknown stage %q: %v",
				ErrInvalidPipeline,
				node.ID,
				i,
				block.Stages[i].Stage,
				err,
			)
		}
	}
	return nil
}

func detectProjectRoot(startDir string) string {
	dir := startDir
	for {
		if fileExists(filepath.Join(dir, "go.mod")) || dirExists(filepath.Join(dir, ".git")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return startDir
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func findCycle(edges map[string][]string) []string {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)

	state := map[string]int{}
	stack := []string{}
	stackIndex := map[string]int{}

	var visit func(node string) []string
	visit = func(node string) []string {
		state[node] = visiting
		stackIndex[node] = len(stack)
		stack = append(stack, node)

		for _, dep := range edges[node] {
			switch state[dep] {
			case unvisited:
				if cycle := visit(dep); len(cycle) > 0 {
					return cycle
				}
			case visiting:
				start := stackIndex[dep]
				cycle := append([]string(nil), stack[start:]...)
				cycle = append(cycle, dep)
				return cycle
			}
		}

		stack = stack[:len(stack)-1]
		delete(stackIndex, node)
		state[node] = done
		return nil
	}

	for node := range edges {
		if state[node] == unvisited {
			if cycle := visit(node); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

// UnmarshalYAML supports provider lists in both forms:
// providers: [claude, codex]
// providers: [{name: claude, model: opus}, ...]
func (p *ProviderList) UnmarshalYAML(value *yaml.Node) error {
	if value == nil {
		*p = nil
		return nil
	}
	if value.Kind != yaml.SequenceNode {
		return fmt.Errorf("providers must be a sequence")
	}

	list := make([]ProviderConfig, 0, len(value.Content))
	for i, node := range value.Content {
		switch node.Kind {
		case yaml.ScalarNode:
			name := strings.TrimSpace(node.Value)
			if name == "" {
				return fmt.Errorf("providers[%d] is empty", i)
			}
			list = append(list, ProviderConfig{Name: name})
		case yaml.MappingNode:
			var cfg ProviderConfig
			if err := node.Decode(&cfg); err != nil {
				return fmt.Errorf("decode providers[%d]: %w", i, err)
			}
			if strings.TrimSpace(cfg.Name) == "" {
				return fmt.Errorf("providers[%d].name is required", i)
			}
			list = append(list, cfg)
		default:
			return fmt.Errorf("providers[%d] must be string or object", i)
		}
	}

	*p = ProviderList(list)
	return nil
}
