package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/hostcancel"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/provider"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
	"os"
	"os/signal"
	"syscall"
)

var version = "development"

func usage() {
	fmt.Fprintln(os.Stderr, "usage: devsy-provider-docker-sandbox init|create|start|stop|delete|status|describe|command|doctor|recover|version [--machine-key KEY]")
}
func execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return compat.Fail("E_CONFIG_DRIFT", "missing subcommand")
	}
	verb := args[0]
	if verb == "version" {
		_, e := fmt.Fprintln(os.Stdout, version)
		return compat.Wrap("E_STORAGE", e)
	}
	switch verb {
	case "init", "create", "start", "stop", "delete", "status", "describe", "command", "doctor", "recover":
	default:
		usage()
		return compat.Fail("E_CONFIG_DRIFT", "unknown subcommand %q", verb)
	}
	profile, e := compat.Load()
	if e != nil {
		return e
	}
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	key := flags.String("machine-key", "", "full SHA-256 machine key")
	attempt := flags.String("attempt", "", "exact child attempt ID")
	operation := flags.String("operation", "", "exact operation ID")
	ackClient := flags.Bool("acknowledge-client-terminated", false, "I terminated the orphaned client or restarted the host")
	ackBackend := flags.Bool("acknowledge-backend-settled", false, "I resolved the Docker-side operation and confirm it has ended")
	list := flags.Bool("list", false, "list local provider journals")
	_ = flags.Bool("json", false, "render provider-owned JSON")
	if e = flags.Parse(args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return compat.Fail("E_CONFIG_DRIFT", "unexpected positional arguments")
	}
	if verb == "doctor" {
		if *list {
			out, e := provider.List(ctx, profile)
			if e != nil {
				return e
			}
			_, e = fmt.Fprintln(os.Stdout, string(out))
			return compat.Wrap("E_STORAGE", e)
		}
		if *key == "" {
			return compat.Fail("E_STATE_MISSING", "doctor requires --machine-key KEY, or --list")
		}
		diagnosticConfig, resolveErr := config.LoadScoped(os.Getenv)
		if resolveErr != nil {
			diagnosticConfig = config.Config{}
		}
		p, e := provider.FromKey(diagnosticConfig, profile, *key)
		if e != nil {
			return e
		}
		out, e := p.Doctor(ctx)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(os.Stdout, string(out))
		return compat.Wrap("E_STORAGE", e)
	}
	if verb == "recover" && *key != "" && *attempt != "" && *ackClient && *operation == "" && !*ackBackend {
		p, e := provider.FromKey(config.Config{}, profile, *key)
		if e != nil {
			return e
		}
		return p.RecoverAttempt(ctx, *attempt)
	}
	var cfg config.Config
	if *key != "" {
		if verb != "delete" && verb != "recover" {
			return compat.Fail("E_STATE_MISSING", "--machine-key only applies to delete and recover")
		}
		cfg, e = config.LoadScoped(os.Getenv)
	} else {
		cfg, e = config.Load(os.Getenv)
	}
	if e != nil {
		return e
	}
	if verb == "init" {
		return provider.Init(ctx, cfg, profile)
	}
	var p provider.Provider
	if *key != "" {
		p, e = provider.FromKey(cfg, profile, *key)
	} else {
		p, e = provider.New(cfg, profile)
	}
	if e != nil {
		return e
	}
	switch verb {
	case "create":
		return p.Create(ctx)
	case "start":
		return p.Start(ctx)
	case "stop":
		return p.Stop(ctx)
	case "delete":
		return p.Delete(ctx)
	case "status":
		token, e := p.Status(ctx)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(os.Stdout, token)
		return compat.Wrap("E_STORAGE", e)
	case "describe":
		description, e := p.Describe(ctx)
		if e != nil {
			return e
		}
		_, e = fmt.Fprint(os.Stdout, description)
		return compat.Wrap("E_STORAGE", e)
	case "command":
		program, ok := os.LookupEnv("COMMAND")
		if !ok {
			return compat.Fail("E_CONFIG_DRIFT", "COMMAND environment entry is required")
		}
		return p.Command(ctx, program, os.Stdin, os.Stdout, os.Stderr)
	case "recover":
		if *key == "" {
			return compat.Fail("E_STATE_MISSING", "recover requires --machine-key KEY")
		}
		if *operation != "" && *ackBackend && *attempt == "" && !*ackClient {
			return p.RecoverOperation(ctx, *operation)
		}
		return compat.Fail("E_UNCLEAN_ATTEMPT", "recover requires either --attempt ID --acknowledge-client-terminated (after terminating the orphaned client or restarting the host), or --operation ID --acknowledge-backend-settled (after resolving the Docker-side operation)")
	}
	return compat.Fail("E_CONFIG_DRIFT", "unsupported command")
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, join, e := hostcancel.Watch(ctx, os.Getenv("DEVSY_PROVIDER_CANCEL_FILE"))
	if e == nil {
		e = execute(ctx, os.Args[1:])
		if joined := join(); joined != nil {
			e = joined
		}
	}
	if e == nil {
		return
	}
	if errors.Is(e, context.Canceled) {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(130)
	}
	if errors.Is(e, context.DeadlineExceeded) {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(124)
	}
	var tagged *compat.Error
	if !errors.As(e, &tagged) {
		if code, ok := sbx.ExitCode(e); ok {
			os.Exit(code)
		}
	}
	fmt.Fprintln(os.Stderr, e)
	os.Exit(1)
}
