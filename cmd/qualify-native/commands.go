package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/qualify"
)

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func preflight(args []string) error {
	fs := newFlags("preflight")
	candidate := fs.String("candidate", "", "candidate commit")
	sbx := fs.String("sbx", "", "sbx executable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	profile, err := compat.Load()
	if err != nil {
		return err
	}
	if !qualify.ValidCommit(*candidate) {
		return errors.New("--candidate must be a 40 character commit")
	}
	head, dirty, err := gitState()
	if err != nil {
		return err
	}
	data, err := sbxVersionJSON(*sbx)
	if err != nil {
		return err
	}
	_, tupleErr := qualify.ParseTuple(profile, data)
	return errors.Join(sourceErrors(head, *candidate, dirty), tupleErr, hostErrors(profile))
}

func sourceErrors(head, candidate string, dirty bool) error {
	var errs []error
	if dirty {
		errs = append(errs, errors.New("working tree is dirty"))
	}
	if head != candidate {
		errs = append(errs, fmt.Errorf("HEAD %s is not the candidate commit %s", head, candidate))
	}
	return errors.Join(errs...)
}

func hostErrors(profile compat.Profile) error {
	for _, h := range profile.QualifiedHosts {
		if h == currentHost() {
			return nil
		}
	}
	return fmt.Errorf("host %s is not listed in qualified_hosts", currentHost())
}

type writeFlags struct {
	candidate, core, version, sbx, log, before, out string
	started, ended, selection                       string
	exit                                            int
}

func parseWriteFlags(args []string) (writeFlags, error) {
	var w writeFlags
	fs := newFlags("write")
	fs.StringVar(&w.candidate, "candidate", "", "candidate commit")
	fs.StringVar(&w.core, "core-ref", "", "core commit")
	fs.StringVar(&w.version, "version", "", "provider version")
	fs.StringVar(&w.sbx, "sbx", "", "sbx executable")
	fs.StringVar(&w.log, "log", "", "captured native test log")
	fs.StringVar(&w.before, "sbx-before", "", "sbx version output captured before the run")
	fs.StringVar(&w.out, "out", "", "evidence file")
	fs.StringVar(&w.started, "started", "", "RFC 3339 start time")
	fs.StringVar(&w.ended, "ended", "", "RFC 3339 end time")
	fs.StringVar(&w.selection, "selection", qualify.Selection, "test selection that ran")
	fs.IntVar(&w.exit, "exit-code", -1, "exit status of go test")
	return w, fs.Parse(args)
}

func (w writeFlags) run() (qualify.Run, error) {
	r := qualify.Run{
		Candidate:  w.candidate,
		CoreCommit: w.core,
		Version:    w.version,
		Selection:  w.selection,
		Partial:    w.selection != qualify.Selection,
		Host:       currentHost(),
		ExitCode:   w.exit,
	}
	var errs []error
	var err error
	r.Started, err = time.Parse(time.RFC3339, w.started)
	errs = append(errs, err)
	r.Ended, err = time.Parse(time.RFC3339, w.ended)
	errs = append(errs, err)
	r.Head, r.Dirty, err = gitState()
	errs = append(errs, err)
	r.HostOSVersion, err = hostOSVersion()
	errs = append(errs, err)
	r.Hostname, err = hostname()
	errs = append(errs, err)
	r.SBXAfter, err = sbxVersionJSON(w.sbx)
	errs = append(errs, err)
	r.SBXBefore, err = readFile(w.before)
	errs = append(errs, err)
	r.Log, err = readFile(w.log)
	errs = append(errs, err)
	return r, errors.Join(errs...)
}

func write(args []string) error {
	w, err := parseWriteFlags(args)
	if err != nil {
		return err
	}
	profile, err := compat.Load()
	if err != nil {
		return err
	}
	r, err := w.run()
	if err != nil {
		return err
	}
	tuple, err := qualify.ParseTuple(profile, r.SBXAfter)
	if err != nil {
		return err
	}
	data, err := readFile(
		filepath.Join("internal", "sbx", "testdata", tuple.Client, "provenance.json"),
	)
	if err != nil {
		return err
	}
	r.ProvenanceSHA256 = qualify.SHA256Hex(data)
	host, err := qualify.Build(profile, r)
	if err != nil {
		return fmt.Errorf("refusing to write evidence: %w", err)
	}
	return writeEvidence(w.out, r, host)
}

func writeEvidence(path string, r qualify.Run, host qualify.Host) error {
	var existing *qualify.Evidence
	if data, err := readFile(path); err == nil {
		existing = new(qualify.Evidence)
		if err := json.Unmarshal(data, existing); err != nil {
			return fmt.Errorf("unreadable existing evidence: %w", err)
		}
	}
	ev, err := qualify.Merge(existing, r, host)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'))
}

func verify(args []string) error {
	fs := newFlags("verify")
	file := fs.String("evidence", "", "evidence file")
	candidate := fs.String("candidate", "", "candidate commit")
	core := fs.String("core-ref", "", "core commit")
	version := fs.String("version", "", "provider version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	profile, err := compat.Load()
	if err != nil {
		return err
	}
	data, err := readFile(*file)
	if err != nil {
		return err
	}
	var ev qualify.Evidence
	if err := json.Unmarshal(data, &ev); err != nil {
		return fmt.Errorf("unreadable evidence: %w", err)
	}
	return qualify.Verify(
		profile,
		ev,
		qualify.Expect{Candidate: *candidate, CoreCommit: *core, Version: *version},
	)
}
