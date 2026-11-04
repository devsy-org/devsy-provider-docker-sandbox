package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"time"
)

type Sandbox struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Detached   bool     `json:"detached"`
	Workspaces []string `json:"workspaces"`
	Template   string   `json:"template"`
}
type Inventory []Sandbox

func (c Client) Inventory(ctx context.Context, p compat.Profile) (Inventory, error) {
	if e := p.RequireInventorySchema(); e != nil {
		return nil, e
	}
	data, e := c.Control(ctx, 20*time.Second, []string{"ls", "--format", "json"}, nil)
	if e != nil {
		return nil, compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	return decodeInventory(data, p)
}

func decodeInventory(data []byte, p compat.Profile) (Inventory, error) {
	var entries []json.RawMessage
	if e := exact(data, &entries); e != nil || entries == nil {
		return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx inventory JSON")
	}
	inventory := make(Inventory, 0, len(entries))
	ids := make(map[string]bool, len(entries))
	names := make(map[string]bool, len(entries))
	for _, raw := range entries {
		fields, e := objectFields(raw)
		if e != nil {
			return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx inventory object")
		}
		var id, name, status string
		if json.Unmarshal(fields["id"], &id) != nil || id == "" ||
			json.Unmarshal(fields["name"], &name) != nil || name == "" ||
			json.Unmarshal(fields["status"], &status) != nil || status == "" {
			return nil, compat.Fail("E_SBX_SCHEMA", "missing sbx inventory identity or status")
		}
		if ids[id] || names[name] {
			return nil, compat.Fail("E_SBX_SCHEMA", "duplicate sbx inventory identity")
		}
		ids[id] = true
		names[name] = true
		kind, ok := p.InventoryStates[status]
		if !ok || (kind != "running" && kind != "stopped" && kind != "transitional") {
			return nil, compat.Fail("E_SBX_STATE_UNKNOWN", "unqualified sbx state %q", status)
		}
		var detached bool
		if value, present := fields["detached"]; present && (string(value) == "null" || json.Unmarshal(value, &detached) != nil) {
			return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx detached field")
		}
		var workspaces []string
		if value, present := fields["workspaces"]; present {
			if json.Unmarshal(value, &workspaces) != nil || workspaces == nil {
				return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx workspaces field")
			}
			for _, workspace := range workspaces {
				if workspace == "" {
					return nil, compat.Fail("E_SBX_SCHEMA", "empty sbx workspace path")
				}
			}
		}
		if kind == "running" && !detached {
			return nil, compat.Fail("E_SBX_SCHEMA", "running sandbox is not detached")
		}
		inventory = append(inventory, Sandbox{ID: id, Name: name, State: status, Detached: detached, Workspaces: workspaces})
	}
	return inventory, nil
}
func objectFields(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, compat.Fail("E_SBX_SCHEMA", "expected sbx JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		key, e := d.Token()
		if e != nil {
			return nil, e
		}
		name, ok := key.(string)
		if !ok {
			return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx JSON field name")
		}
		if _, present := fields[name]; present {
			return nil, compat.Fail("E_SBX_SCHEMA", "duplicate sbx JSON field %q", name)
		}
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return nil, e
		}
		fields[name] = value
	}
	token, e = d.Token()
	if e != nil || token != json.Delim('}') {
		return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx JSON object end")
	}
	return fields, nil
}

func (items Inventory) Find(name string) (Sandbox, bool) {
	for _, s := range items {
		if s.Name == name {
			return s, true
		}
	}
	return Sandbox{}, false
}
