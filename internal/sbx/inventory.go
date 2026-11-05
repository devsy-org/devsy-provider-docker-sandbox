package sbx

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
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
	data, e := c.Control(ctx, 20*time.Second, []string{"ls", formatArg, jsonFormat}, nil)
	if e != nil {
		return nil, compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	return decodeInventory(data)
}

func decodeInventory(data []byte) (Inventory, error) {
	var entries []json.RawMessage
	if e := exact(data, &entries); e != nil || entries == nil {
		return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx inventory JSON")
	}
	inventory := make(Inventory, 0, len(entries))
	ids := make(map[string]bool, len(entries))
	names := make(map[string]bool, len(entries))
	for _, raw := range entries {
		entry, e := inventoryEntry(raw, ids, names)
		if e != nil {
			return nil, e
		}

		inventory = append(inventory, entry)
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
		name, value, e := readObjectField(d, fields)
		if e != nil {
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

func inventoryEntry(raw json.RawMessage, ids, names map[string]bool) (Sandbox, error) {
	fields, e := objectFields(raw)
	if e != nil {
		return Sandbox{}, compat.Fail("E_SBX_SCHEMA", "invalid sbx inventory object")
	}
	entry, e := inventoryIdentity(fields)
	if e != nil {
		return Sandbox{}, e
	}
	if ids[entry.ID] || names[entry.Name] {
		return Sandbox{}, compat.Fail("E_SBX_SCHEMA", "duplicate sbx inventory identity")
	}
	ids[entry.ID] = true
	names[entry.Name] = true
	return inventoryDetails(fields, entry)
}

func inventoryDetails(fields map[string]json.RawMessage, entry Sandbox) (Sandbox, error) {
	var detached bool
	if value, present := fields["detached"]; present &&
		(string(value) == "null" || json.Unmarshal(value, &detached) != nil) {
		return Sandbox{}, compat.Fail("E_SBX_SCHEMA", "invalid sbx detached field")
	}
	var workspaces []string
	if value, present := fields["workspaces"]; present {
		if json.Unmarshal(value, &workspaces) != nil || workspaces == nil {
			return Sandbox{}, compat.Fail("E_SBX_SCHEMA", "invalid sbx workspaces field")
		}
		if slices.Contains(workspaces, "") {
			return Sandbox{}, compat.Fail("E_SBX_SCHEMA", "empty sbx workspace path")
		}
	}
	entry.Detached = detached
	entry.Workspaces = workspaces
	return entry, nil
}

func readObjectField(
	d *json.Decoder,
	fields map[string]json.RawMessage,
) (string, json.RawMessage, error) {
	key, e := d.Token()
	if e != nil {
		return "", nil, e
	}
	name, ok := key.(string)
	if !ok {
		return "", nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx JSON field name")
	}
	if _, present := fields[name]; present {
		return "", nil, compat.Fail("E_SBX_SCHEMA", "duplicate sbx JSON field %q", name)
	}
	var value json.RawMessage
	if e = d.Decode(&value); e != nil {
		return "", nil, e
	}
	return name, value, nil
}

func inventoryIdentity(fields map[string]json.RawMessage) (Sandbox, error) {
	var id, name, status string
	if json.Unmarshal(fields["id"], &id) != nil || id == "" ||
		json.Unmarshal(fields["name"], &name) != nil || name == "" ||
		json.Unmarshal(fields["status"], &status) != nil || status == "" {
		return Sandbox{}, compat.Fail("E_SBX_SCHEMA", "missing sbx inventory identity or status")
	}
	return Sandbox{ID: id, Name: name, State: status}, nil
}
