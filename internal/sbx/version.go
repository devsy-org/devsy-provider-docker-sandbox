package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

type ClientVersion struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
}

type Version struct {
	Client ClientVersion   `json:"client"`
	Server json.RawMessage `json:"server"`
}

func exact(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return errors.New("extra structured output")
	}
	return nil
}

func (c Client) Version(ctx context.Context) (Version, error) {
	data, e := c.Control(ctx, 10*time.Second, []string{"version", formatArg, jsonFormat}, nil)
	if e != nil {
		return Version{}, compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	var v Version
	if e = exact(data, &v); e != nil || v.Client.Version == "" || len(v.Server) == 0 {
		return v, compat.Fail("E_SBX_SCHEMA", "invalid sbx version JSON")
	}
	return v, nil
}

func DaemonVersion(v Version, p compat.Profile) (string, error) {
	if e := p.RequireDaemonSchema(); e != nil {
		return "", e
	}
	var server struct {
		Version string `json:"version"`
		State   string `json:"state"`
	}
	if e := json.Unmarshal(
		v.Server,
		&server,
	); e != nil || server.Version == "" ||
		server.State != "running" {
		return "", compat.Fail(
			"E_SBX_VERSION",
			"successful-server daemon version unavailable",
		)
	}
	return server.Version, nil
}
