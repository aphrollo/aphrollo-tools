package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/aphrollo/aphrollo-tools/internal/dev"
)

const devUsage = `usage: aphrollo dev <subcommand> [args]

Subcommands:
  up                start the whole dev tier
  down [--all]      stop api+rlndx (--all also stops infra)
  restart <svc>     restart one of: api | rlndx | infra
  status            show dev-tier unit status
  logs [<svc>]      journal for one dev unit, or all (default 200 lines, -n N)

This is a service control plane: up/down/restart execute immediately (like
systemctl). status/logs are read-only. Only the write verbs need privilege —
status works unprivileged, logs via the systemd-journal group, and up/down/
restart via exact-match systemctl sudoers grants (no wildcards).
`

func runDev(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, devUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, devUsage)
		return 0
	case "up":
		if len(rest) != 0 {
			fmt.Fprintf(stderr, "aphrollo: dev up takes no arguments\n")
			return 2
		}
		return devResult(dev.Up(stdout, stderr), stderr)
	case "down":
		all := false
		switch {
		case len(rest) == 0:
		case len(rest) == 1 && rest[0] == "--all":
			all = true
		default:
			fmt.Fprintf(stderr, "aphrollo: usage: dev down [--all]\n")
			return 2
		}
		return devResult(dev.Down(all, stdout, stderr), stderr)
	case "restart":
		if len(rest) != 1 {
			fmt.Fprintf(stderr, "aphrollo: usage: dev restart <api|rlndx|infra>\n")
			return 2
		}
		if isHelpArg(rest[0]) {
			fmt.Fprint(stdout, devUsage)
			return 0
		}
		// dev.Restart is this binary's one privileged atom (exact-match
		// systemctl restart), so a help flag must never reach it — today
		// unitFor's whitelist refuses "--help" anyway, but that is an
		// accident of the whitelist, not a guarantee every future unit-name
		// check keeps.
		return devResult(dev.Restart(rest[0], stdout, stderr), stderr)
	case "status":
		if len(rest) != 0 {
			fmt.Fprintf(stderr, "aphrollo: dev status takes no arguments\n")
			return 2
		}
		return devResult(dev.Status(stdout, stderr), stderr)
	case "logs":
		return runDevLogs(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "aphrollo dev: unknown subcommand %q\n\n%s", sub, devUsage)
		return 2
	}
}

func runDevLogs(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 200, "number of journal lines to show")
	pos, err := parseFlagsAnywhere(fs, args)
	if err != nil {
		return 2
	}
	svc := ""
	switch len(pos) {
	case 0:
	case 1:
		svc = pos[0]
	default:
		fmt.Fprintf(stderr, "aphrollo: usage: dev logs [<svc>] [-n N]\n")
		return 2
	}
	return devResult(dev.Logs(svc, *n, stdout, stderr), stderr)
}

// devResult classifies a dev error by SENTINEL, never by message text: a bad svc token is a usage error (2), anything else is runtime (1).
func devResult(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(stderr, "aphrollo: %v\n", err)
	if errors.Is(err, dev.ErrServiceNotAllowed) || errors.Is(err, dev.ErrServiceRequired) {
		return 2
	}
	return 1
}
