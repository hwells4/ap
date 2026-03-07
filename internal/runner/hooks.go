package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	apexec "github.com/hwells4/ap/internal/exec"
	"github.com/hwells4/ap/internal/store"
)

// LifecycleHooks defines shell commands at runner lifecycle points.
type LifecycleHooks struct {
	PreSession    string
	PreIteration  string
	PreStage      string
	PostIteration string
	PostStage     string
	PostSession   string
	OnFailure     string
}

// IsEmpty reports whether all hook fields are empty.
func (h LifecycleHooks) IsEmpty() bool {
	return h.PreSession == "" &&
		h.PreIteration == "" &&
		h.PreStage == "" &&
		h.PostIteration == "" &&
		h.PostStage == "" &&
		h.PostSession == "" &&
		h.OnFailure == ""
}

// Command returns the shell command for a hook name, or "" if unset.
func (h LifecycleHooks) Command(name string) string {
	switch name {
	case "pre_session":
		return h.PreSession
	case "pre_iteration":
		return h.PreIteration
	case "pre_stage":
		return h.PreStage
	case "post_iteration":
		return h.PostIteration
	case "post_stage":
		return h.PostStage
	case "post_session":
		return h.PostSession
	case "on_failure":
		return h.OnFailure
	default:
		return ""
	}
}

// ApplyOverrides merges a map of hook name → command into the struct.
// Only non-empty values override existing values.
func (h *LifecycleHooks) ApplyOverrides(overrides map[string]string) {
	for _, name := range []string{
		"pre_session", "pre_iteration", "pre_stage",
		"post_iteration", "post_stage", "post_session",
		"on_failure",
	} {
		if v := strings.TrimSpace(overrides[name]); v != "" {
			h.set(name, v)
		}
	}
}

// set assigns a command to a hook by name.
func (h *LifecycleHooks) set(name, command string) {
	switch name {
	case "pre_session":
		h.PreSession = command
	case "pre_iteration":
		h.PreIteration = command
	case "pre_stage":
		h.PreStage = command
	case "post_iteration":
		h.PostIteration = command
	case "post_stage":
		h.PostStage = command
	case "post_session":
		h.PostSession = command
	case "on_failure":
		h.OnFailure = command
	}
}

// HookContext tracks accumulated state during a session run.
// Every variable is available to every hook — no special-casing.
type HookContext struct {
	cfg     Config
	stage   string
	iter    int
	status  string
	summary string // last iteration summary
}

// NewHookContext creates a HookContext for a session.
func NewHookContext(cfg Config) *HookContext {
	return &HookContext{cfg: cfg, status: "running"}
}

// SetStage updates the current stage name.
func (hc *HookContext) SetStage(stage string) { hc.stage = stage }

// SetIteration updates the current iteration number.
func (hc *HookContext) SetIteration(iter int) { hc.iter = iter }

// SetStatus updates the session status.
func (hc *HookContext) SetStatus(status string) { hc.status = status }

// SetSummary updates the last iteration summary.
func (hc *HookContext) SetSummary(summary string) { hc.summary = summary }

// Fire executes a named lifecycle hook. Non-fatal: emits events but never
// returns an error to the caller.
func (hc *HookContext) Fire(ctx context.Context, hookName string) {
	command := hc.cfg.Hooks.Command(hookName)
	if strings.TrimSpace(command) == "" {
		return
	}
	vars := hc.vars()
	if err := RunHook(ctx, hookName, command, hc.cfg.WorkDir, vars, hc.cfg.HookTimeout); err != nil {
		emitEvent(ctx, hc.cfg, store.TypeHookFailed, "{}", map[string]any{
			"hook":  hookName,
			"error": err.Error(),
		})
	} else {
		emitEvent(ctx, hc.cfg, store.TypeHookCompleted, "{}", map[string]any{
			"hook": hookName,
		})
	}
}

// vars builds the full variable map. Every variable is always present.
func (hc *HookContext) vars() map[string]string {
	return map[string]string{
		"SESSION":   hc.cfg.Session,
		"STAGE":     hc.stage,
		"ITERATION": strconv.Itoa(hc.iter),
		"STATUS":    hc.status,
		"SUMMARY":   hc.summary,
	}
}

// RunHook executes a lifecycle hook command via sh -c.
// Returns nil on empty command. Non-fatal: returns error but caller decides.
func RunHook(ctx context.Context, name, command, workDir string, vars map[string]string, timeout time.Duration) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}

	// Substitute ${VAR} placeholders.
	if len(vars) > 0 {
		pairs := make([]string, 0, len(vars)*2)
		for k, v := range vars {
			pairs = append(pairs, "${"+k+"}", v)
		}
		command = strings.NewReplacer(pairs...).Replace(command)
	}

	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workDir

	// Inherit parent environment and add AP_* variables.
	cmd.Env = os.Environ()
	for k, v := range vars {
		cmd.Env = append(cmd.Env, fmt.Sprintf("AP_%s=%s", k, v))
	}

	result, err := apexec.Run(ctx, cmd, apexec.Options{
		MaxOutput:   64 * 1024,       // 64 KiB cap for hook output
		MinTime:     0,               // don't fail-fast on short remaining time
		GracePeriod: 5 * time.Second, // short grace period for hooks
	})
	if err != nil {
		stdout := ""
		stderr := ""
		if result != nil {
			stdout = strings.TrimSpace(string(result.Stdout))
			stderr = strings.TrimSpace(string(result.Stderr))
		}
		detail := err.Error()
		if stderr != "" {
			detail += ": " + stderr
		} else if stdout != "" {
			detail += ": " + stdout
		}
		return fmt.Errorf("hook %s failed: %s", name, detail)
	}
	return nil
}
