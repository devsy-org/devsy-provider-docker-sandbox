package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/hostcancel"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/provider"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/sbx"
)

var version = "development"

const (
	recoverVerb = "recover"
	initVerb    = "init"
)

func usage() {
	fmt.Fprintln(
		os.Stderr,
		"usage: devsy-provider-docker-sandbox init|create|start|stop|delete|status|describe|"+
			"command|doctor|recover|version [--machine-key KEY]",
	)
}

type options struct {
	key, attempt, operation     string
	ackClient, ackBackend, list bool
}

type invocation struct {
	ctx     context.Context
	profile compat.Profile
	options options
	p       provider.Provider
}

type handler func(invocation) error

var handlers = map[string]handler{
	initVerb: runInit, "doctor": runDoctor, recoverVerb: runRecover,
	"create": func(i invocation) error { return i.p.Create(i.ctx) },
	"start":  func(i invocation) error { return i.p.Start(i.ctx) },
	"stop":   func(i invocation) error { return i.p.Stop(i.ctx) },
	"delete": func(i invocation) error { return i.p.Delete(i.ctx) },
	"status": runStatus, "describe": runDescribe, "command": runCommand,
}

func parseOptions(verb string, args []string) (options, error) {
	var o options
	flags := flag.NewFlagSet(verb, flag.ContinueOnError)
	flags.StringVar(&o.key, "machine-key", "", "full SHA-256 machine key")
	flags.StringVar(&o.attempt, "attempt", "", "exact child attempt ID")
	flags.StringVar(&o.operation, "operation", "", "exact operation ID")
	flags.BoolVar(
		&o.ackClient,
		"acknowledge-client-terminated",
		false,
		"I terminated the orphaned client or restarted the host",
	)
	flags.BoolVar(
		&o.ackBackend,
		"acknowledge-backend-settled",
		false,
		"I resolved the Docker-side operation and confirm it has ended",
	)
	flags.BoolVar(&o.list, "list", false, "list local provider journals")
	_ = flags.Bool("json", false, "render provider-owned JSON")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, compat.Fail("E_CONFIG_DRIFT", "unexpected positional arguments")
	}
	return o, nil
}

func execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return compat.Fail("E_CONFIG_DRIFT", "missing subcommand")
	}
	verb := args[0]
	if verb == "version" {
		_, err := fmt.Fprintln(os.Stdout, version)
		return compat.Wrap("E_STORAGE", err)
	}
	h, ok := handlers[verb]
	if !ok {
		usage()
		return compat.Fail("E_CONFIG_DRIFT", "unknown subcommand %q", verb)
	}
	profile, err := compat.Load()
	if err != nil {
		return err
	}
	o, err := parseOptions(verb, args[1:])
	if err != nil {
		return err
	}
	return dispatch(invocation{ctx: ctx, profile: profile, options: o}, verb, h)
}

func (o options) clientRecovery() bool {
	return o.attempt != "" && o.ackClient && o.operation == "" && !o.ackBackend
}

func (o options) backendRecovery() bool {
	return o.operation != "" && o.ackBackend && o.attempt == "" && !o.ackClient
}

func bypassConfig(verb string, o options) bool {
	return verb == "doctor" || (verb == recoverVerb && o.key != "" && o.clientRecovery())
}

func dispatch(i invocation, verb string, h handler) error {
	o := i.options
	if bypassConfig(verb, o) {
		return h(i)
	}
	warnUnqualifiedHost(os.Stderr, i.profile, verb)
	cfg, err := loadConfig(verb, o.key)
	if err != nil {
		return err
	}
	if verb == initVerb {
		return provider.Init(i.ctx, cfg, i.profile)
	}
	if o.key != "" {
		i.p, err = provider.FromKey(cfg, i.profile, o.key)
	} else {
		i.p, err = provider.New(cfg, i.profile)
	}
	if err != nil {
		return err
	}
	return h(i)
}

func warnUnqualifiedHost(w io.Writer, profile compat.Profile, verb string) {
	if verb != initVerb && verb != "create" && verb != "start" {
		return
	}
	if warning := profile.HostWarning(); warning != "" {
		_, _ = fmt.Fprintln(w, "warning: "+warning)
	}
}

func loadConfig(verb, key string) (config.Config, error) {
	if key == "" {
		return config.Load(os.Getenv)
	}
	if verb != "delete" && verb != recoverVerb {
		return config.Config{}, compat.Fail(
			"E_STATE_MISSING",
			"--machine-key only applies to delete and recover",
		)
	}
	return config.LoadScoped(os.Getenv)
}

func runInit(invocation) error { return compat.Fail("E_CONFIG_DRIFT", "unsupported command") }
func printOutput(out []byte, err error) error {
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(out))
	return compat.Wrap("E_STORAGE", err)
}

func runDoctor(i invocation) error {
	if i.options.list {
		out, err := provider.List(i.ctx, i.profile)
		return printOutput(out, err)
	}
	if i.options.key == "" {
		return compat.Fail("E_STATE_MISSING", "doctor requires --machine-key KEY, or --list")
	}
	cfg, err := config.LoadScoped(os.Getenv)
	if err != nil {
		cfg = config.Config{}
	}
	p, err := provider.FromKey(cfg, i.profile, i.options.key)
	if err != nil {
		return err
	}
	out, err := p.Doctor(i.ctx)
	return printOutput(out, err)
}

func runRecover(i invocation) error {
	o := i.options
	if o.key == "" {
		return compat.Fail("E_STATE_MISSING", "recover requires --machine-key KEY")
	}
	if o.clientRecovery() {
		p, err := provider.FromKey(config.Config{}, i.profile, o.key)
		if err != nil {
			return err
		}
		return p.RecoverAttempt(i.ctx, o.attempt)
	}
	if o.backendRecovery() {
		return i.p.RecoverOperation(i.ctx, o.operation)
	}
	return compat.Fail(
		"E_UNCLEAN_ATTEMPT",
		"recover requires either --attempt ID --acknowledge-client-terminated "+
			"(after terminating the orphaned client or restarting the host), or --operation ID "+
			"--acknowledge-backend-settled (after resolving the Docker-side operation)",
	)
}

func runStatus(i invocation) error {
	token, err := i.p.Status(i.ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, token)
	return compat.Wrap("E_STORAGE", err)
}

func runDescribe(i invocation) error {
	description, err := i.p.Describe(i.ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(os.Stdout, description)
	return compat.Wrap("E_STORAGE", err)
}

func runCommand(i invocation) error {
	program, ok := os.LookupEnv("COMMAND")
	if !ok {
		return compat.Fail("E_CONFIG_DRIFT", "COMMAND environment entry is required")
	}
	return i.p.Command(
		i.ctx,
		program,
		provider.CommandStreams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr},
	)
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, join, err := hostcancel.Watch(ctx, os.Getenv("DEVSY_PROVIDER_CANCEL_FILE"))
	if err == nil {
		err = execute(ctx, os.Args[1:])
		if joined := join(); joined != nil {
			err = joined
		}
	}
	return exitCode(err)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		if _, ok := errors.AsType[*compat.Error](err); ok {
			fmt.Fprintln(os.Stderr, err)
		}
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) {
		fmt.Fprintln(os.Stderr, err)
		return 124
	}
	if _, ok := errors.AsType[*compat.Error](err); !ok {
		if code, ok := sbx.ExitCode(err); ok {
			return code
		}
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}
func main() { os.Exit(run()) }
