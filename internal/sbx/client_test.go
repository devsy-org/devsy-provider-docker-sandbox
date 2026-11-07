package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/state"
)

const (
	testJSON          = "json"
	testVersion       = "v0.50.1"
	testFormat        = "--format"
	testRunning       = "running"
	testStopped       = "stopped"
	testOwned         = "owned"
	testSchema        = "E_SBX_SCHEMA"
	testShellOverride = "E_SHELL_OVERRIDE"
	testLiteral       = "literal"
	testNull          = `null`
)

const fakeSource = `package main
import("encoding/json";"io";"os";"time";"strings")
func main(){if len(os.Args)<2{os.Exit(1)};
if os.Getenv("RESTART_ERROR")!=""{os.Stderr.WriteString(os.Getenv("RESTART_ERROR"));os.Exit(1)};
switch os.Args[1]{
case "version":
os.Stdout.WriteString("{\"client\":{\"version\":\"v0.50.1\",\"revision\":\"observed\"}," +
 "\"server\":{\"state\":\"unavailable\"}}")
case "ls":os.Stdout.WriteString("[]")
case "settings":if len(os.Args)!=5||os.Args[2]!="get"||os.Args[3]!="--json"||
 os.Args[4]!="kit.shortcuts.shell"{os.Exit(69)};
if os.Getenv("SHELL_SETTING_FAIL")!=""{os.Exit(71)};
if v:=os.Getenv("SHELL_SETTING_JSON");
v!=""{os.Stdout.WriteString(v)}else{os.Stdout.WriteString("{\"default\":\"\",\"description\":\"shell shortcut\"," +
 "\"key\":\"kit.shortcuts.shell\",\"source\":\"default\",\"type\":\"string\",\"value\":\"\"}")}
case "exec":if os.Getenv("COMMAND")!=""||os.Getenv("DEVSY_PROVIDER_CANCEL_FILE")!=""{os.Exit(70)};
switch os.Args[len(os.Args)-1]{case "byte-test":_,_=io.Copy(os.Stdout,os.Stdin);
os.Stderr.Write([]byte{0,255,128,13,10});
case "exit-seven":os.Exit(7);
case "wait":time.Sleep(15*time.Second);
default:json.NewEncoder(os.Stdout).Encode(map[string]string{"command":os.Args[len(os.Args)-1],
"cwd":func()string{p,_:=os.Getwd();
return p}()})}
default:if strings.Contains(os.Args[1],"--all"){os.Exit(2)};os.Exit(1)}}`

func fakeBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if e := os.WriteFile(source, []byte(fakeSource), 0o600); e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(dir, "fake-sbx")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	out, e := exec.Command("go", "build", "-o", binary, source).CombinedOutput()
	if e != nil {
		t.Fatalf("compile fake sbx: %v: %s", e, out)
	}
	return binary
}

func testClient(t *testing.T) Client {
	t.Helper()
	return Client{
		Path: fakeBinary(t),
		Store: state.Store{
			Root: filepath.Join(t.TempDir(), "devsy-provider-docker-sandbox", "state-v1"),
		},
	}
}

func TestFakeVersionAndInventory(t *testing.T) {
	c := testClient(t)
	v, e := c.Version(context.Background())
	if e != nil || v.Client.Version != testVersion {
		t.Fatalf("version failed: %+v %v", v, e)
	}
	data, e := c.Control(
		context.Background(),
		time.Second,
		[]string{"ls", testFormat, testJSON},
		nil,
	)
	if e != nil || string(data) != "[]" {
		t.Fatalf("inventory failed: %s %v", data, e)
	}
}

func TestControlDaemonRestart(t *testing.T) {
	c := testClient(t)
	for _, message := range []string{
		"needs to restart; cannot prompt for restart",
		"needs to restart; try: sbx daemon restart",
	} {
		t.Setenv("RESTART_ERROR", message)
		_, err := c.Control(context.Background(), time.Second, []string{"ls"}, nil)
		var tagged *compat.Error
		if !errors.As(err, &tagged) || tagged.Code != "E_SBX_DAEMON_RESTART" ||
			!strings.Contains(err.Error(), "stops running sandboxes") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

func TestBinaryTransportAndOpaqueCommand(t *testing.T) {
	c := testClient(t)
	t.Setenv("COMMAND", "host environment secret")
	t.Setenv("DEVSY_PROVIDER_CANCEL_FILE", "/tmp/secret")
	input := append(bytes.Repeat([]byte{0, 255, 128, 13, 10}, 2048), 0)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	p, e := c.Start(
		context.Background(),
		StartRequest{
			Args:   ExecArgs(testOwned, "byte-test"),
			Stdin:  bytes.NewReader(input),
			Stdout: stdout,
			Stderr: stderr,
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(stdout.Bytes(), input) ||
		!bytes.Equal(stderr.Bytes(), []byte{0, 255, 128, 13, 10}) {
		t.Fatal("raw streams changed")
	}
}

func TestBinaryInputJSONMarshal(t *testing.T) {
	input := append(bytes.Repeat([]byte{0, 255, 128, 13, 10}, 2048), 0)
	if _, err := json.Marshal(input); err != nil {
		t.Fatal(err)
	}
}

func TestGuestExitCode(t *testing.T) {
	c := testClient(t)
	p, e := c.Start(
		context.Background(),
		StartRequest{
			Args:   ExecArgs(testOwned, "exit-seven"),
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	code, ok := ExitCode(p.Wait(context.Background()))
	if !ok || code != 7 {
		t.Fatalf("wrong exit: %d %v", code, ok)
	}
}

func TestCancellationReapsOwnedClient(t *testing.T) {
	c := testClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	p, e := c.Start(
		ctx,
		StartRequest{Args: ExecArgs(testOwned, "wait"), Stdout: io.Discard, Stderr: io.Discard},
	)
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	e = p.Wait(ctx)
	if !errors.Is(e, context.Canceled) {
		t.Fatalf("wrong cancellation result: %v", e)
	}
}

func TestControlCWDIsPrivate(t *testing.T) {
	c := testClient(t)
	out, e := c.Control(context.Background(), time.Second, ExecArgs(testOwned, "cwd"), nil)
	if e != nil {
		t.Fatal(e)
	}
	var result struct {
		Command string `json:"command"`
		CWD     string `json:"cwd"`
	}
	if e = json.Unmarshal(out, &result); e != nil {
		t.Fatal(e)
	}
	if result.Command != "cwd" || !strings.Contains(result.CWD, "control-cwd") {
		t.Fatalf("unsafe cwd: %+v", result)
	}
}
