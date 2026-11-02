package sbx

import (
	"context"
	"encoding/json"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"time"
)

type Sandbox struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	State      string            `json:"state"`
	Detached   bool              `json:"detached"`
	Workspaces []json.RawMessage `json:"workspaces"`
	Template   string            `json:"template"`
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
	var inventory Inventory
	if e = exact(data, &inventory); e != nil || inventory == nil {
		return nil, compat.Fail("E_SBX_SCHEMA", "invalid sbx inventory JSON")
	}
	ids := map[string]bool{}
	names := map[string]bool{}
	for _, item := range inventory {
		if item.ID == "" || item.Name == "" || ids[item.ID] || names[item.Name] {
			return nil, compat.Fail("E_SBX_SCHEMA", "missing or duplicate sbx identity")
		}
		ids[item.ID] = true
		names[item.Name] = true
		if _, ok := p.InventoryStates[item.State]; !ok {
			return nil, compat.Fail("E_SBX_STATE_UNKNOWN", "unqualified sbx state %q", item.State)
		}
	}
	return inventory, nil
}
func (items Inventory) Find(name string) (Sandbox, bool) {
	for _, s := range items {
		if s.Name == name {
			return s, true
		}
	}
	return Sandbox{}, false
}
