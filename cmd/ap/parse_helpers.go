package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hwells4/ap/internal/output"
	"github.com/hwells4/ap/internal/store"
)

// extraFlagHandler is called for flags that are not --json, --project-root, or
// --workdir. It returns the new loop index and an error response if the flag
// is invalid. Return handled=true if the flag was consumed.
type extraFlagHandler func(flag string, args []string, i int) (nextIndex int, handled bool, errResp *output.ErrorResponse)

// parsedSessionArgs holds the common results of parsing a session subcommand's arguments.
type parsedSessionArgs struct {
	SessionName string
	ProjectRoot string
}

// parseSessionArgs extracts the session name and --project-root/--workdir flag
// from a subcommand's argument list, delegating command-specific flags to the
// provided handler. commandName is used in error messages (e.g. "status"),
// syntax is the usage string for error output.
func parseSessionArgs(args []string, commandName, syntax string, extraFlags extraFlagHandler) (parsedSessionArgs, *output.ErrorResponse) {
	var result parsedSessionArgs

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			continue
		case arg == "--project-root" || strings.HasPrefix(arg, "--project-root=") || arg == "--workdir" || strings.HasPrefix(arg, "--workdir="):
			value, next, err := readFlagValue(arg, args, i)
			if err != nil {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					err.Error(),
					"",
					syntax,
					[]string{fmt.Sprintf("ap %s my-session --project-root /abs/path --json", commandName)},
				)
				return parsedSessionArgs{}, &errResp
			}
			i = next
			result.ProjectRoot = strings.TrimSpace(value)
		case strings.HasPrefix(arg, "-"):
			if extraFlags != nil {
				next, handled, errResp := extraFlags(arg, args, i)
				if errResp != nil {
					return parsedSessionArgs{}, errResp
				}
				if handled {
					i = next
					continue
				}
			}
			errResp := output.NewError(
				"INVALID_ARGUMENT",
				fmt.Sprintf("unknown flag %q", arg),
				fmt.Sprintf("ap %s accepts --project-root/--workdir and --json.", commandName),
				syntax,
				[]string{fmt.Sprintf("ap %s my-session", commandName), fmt.Sprintf("ap %s my-session --project-root /abs/path --json", commandName)},
			)
			return parsedSessionArgs{}, &errResp
		default:
			if result.SessionName != "" {
				errResp := output.NewError(
					"INVALID_ARGUMENT",
					fmt.Sprintf("ap %s takes exactly one session name", commandName),
					fmt.Sprintf("Got %q and %q.", result.SessionName, arg),
					syntax,
					[]string{fmt.Sprintf("ap %s my-session", commandName)},
				)
				return parsedSessionArgs{}, &errResp
			}
			result.SessionName = strings.TrimSpace(arg)
		}
	}

	if result.SessionName == "" {
		errResp := output.NewError(
			"INVALID_ARGUMENT",
			"missing required argument: <session>",
			fmt.Sprintf("Provide the session name to %s.", commandName),
			syntax,
			[]string{fmt.Sprintf("ap %s my-session", commandName), fmt.Sprintf("ap %s my-session --project-root /abs/path --json", commandName)},
		)
		return parsedSessionArgs{}, &errResp
	}

	return result, nil
}

// sessionResolutionOpts configures how session resolution errors are rendered.
type sessionResolutionOpts struct {
	// CommandName is used to build suggestion strings (e.g. "status", "kill").
	CommandName string
	// Syntax is the usage string shown in error responses.
	Syntax string
	// FallbackCode is the error code for generic resolution failures
	// (e.g. "STATE_READ_FAILED", "EVENTS_READ_FAILED", "QUERY_FAILED").
	FallbackCode string
}

// resolveSessionWithErrors calls resolveSessionStore and, on error, renders
// the appropriate structured error response. It returns a non-zero exit code
// on failure. On success it returns the store, cleanup function, and exit 0.
func resolveSessionWithErrors(
	ctx context.Context,
	deps cliDeps,
	sessionName, projectRootFlag string,
	opts sessionResolutionOpts,
) (*store.Store, func(), int) {
	selectedStore, cleanup, lookupErr := resolveSessionStore(ctx, deps, sessionName, projectRootFlag)
	if lookupErr == nil {
		return selectedStore, cleanup, 0
	}

	if errors.Is(lookupErr, errSessionLookupNotFound) {
		code := renderError(deps, output.ExitNotFound, output.NewError(
			"SESSION_NOT_FOUND",
			fmt.Sprintf("session %q not found", sessionName),
			"No session found in local or machine-wide index.",
			opts.Syntax,
			[]string{"ap query sessions --status running --json", fmt.Sprintf("ap %s my-session --project-root /abs/path --json", opts.CommandName)},
		))
		return nil, nil, code
	}

	var ambiguous *sessionLookupAmbiguousError
	if errors.As(lookupErr, &ambiguous) {
		suggestions := []string{}
		for _, match := range ambiguous.Matches {
			suggestions = append(suggestions, fmt.Sprintf("ap %s %s --project-root %s --json", opts.CommandName, sessionName, match.ProjectRoot))
			if len(suggestions) >= 3 {
				break
			}
		}
		code := renderError(deps, output.ExitInvalidArgs, output.NewError(
			"SESSION_AMBIGUOUS",
			lookupErr.Error(),
			"Use --project-root to select the project explicitly.",
			opts.Syntax,
			suggestions,
		))
		return nil, nil, code
	}

	fallbackCode := opts.FallbackCode
	if fallbackCode == "" {
		fallbackCode = "STATE_READ_FAILED"
	}
	code := renderError(deps, output.ExitGeneralError, output.NewError(
		fallbackCode,
		fmt.Sprintf("failed to resolve store for session %q", sessionName),
		lookupErr.Error(),
		opts.Syntax,
		nil,
	))
	return nil, nil, code
}
