package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"io"
	"time"
)

type Version struct {
	Client struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
	} `json:"client"`
	Server json.RawMessage `json:"server"`
}

func exact(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(v); err != nil {
		return err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("extra structured output")
	}
	return nil
}
func (c Client) Version(ctx context.Context) (Version, error) {
	data, e := c.Control(ctx, 10*time.Second, []string{"version", "--format", "json"}, nil)
	if e != nil {
		return Version{}, compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	var v Version
	if e = exact(data, &v); e != nil || v.Client.Version == "" || len(v.Server) == 0 {
		return v, compat.Fail("E_SBX_SCHEMA", "invalid sbx version JSON")
	}
	return v, nil
}
func ServerVersion(v Version, p compat.Profile) (string, error) {
	if e := p.RequireDaemonSchema(); e != nil {
		return "", e
	}
	var server struct {
		Version string `json:"version"`
		State   string `json:"state"`
	}
	if e := json.Unmarshal(v.Server, &server); e != nil || server.Version == "" || server.State != "running" {
		return "", compat.Fail("E_SBX_VERSION", "qualified successful-server daemon version unavailable")
	}
	for _, qualified := range p.QualifiedDaemonVersions[v.Client.Version] {
		if server.Version == qualified {
			return server.Version, nil
		}
	}
	return "", compat.Fail("E_SBX_VERSION", "sbx client %s and daemon %s are not a qualified pair", v.Client.Version, server.Version)
}
