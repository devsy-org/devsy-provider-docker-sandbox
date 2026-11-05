package sbx

import (
	"context"
	"encoding/json"
	"time"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

type Inspection struct {
	ID            string            `json:"id,omitempty"`
	Name          string            `json:"name"`
	State         string            `json:"state"`
	ImageDigest   string            `json:"image_digest"`
	RuntimeMounts []json.RawMessage `json:"runtime_mounts"`
}

func (c Client) Inspect(ctx context.Context, p compat.Profile, name string) (Inspection, error) {
	if e := p.RequireInventorySchema(); e != nil {
		return Inspection{}, e
	}
	data, e := c.Control(ctx, 20*time.Second, []string{"inspect", name, formatArg, jsonFormat}, nil)
	if e != nil {
		return Inspection{}, compat.Wrap("E_SBX_UNAVAILABLE", e)
	}
	return decodeInspection(data, p, name)
}

func decodeInspection(data []byte, p compat.Profile, name string) (Inspection, error) {
	var raw json.RawMessage
	if e := exact(data, &raw); e != nil {
		return Inspection{}, compat.Fail("E_SBX_SCHEMA", "invalid sbx inspect JSON")
	}
	fields, e := objectFields(raw)
	if e != nil {
		return Inspection{}, compat.Fail("E_SBX_SCHEMA", "invalid sbx inspect object")
	}
	inspected, e := inspectionFields(fields)
	if e != nil {
		return Inspection{}, e
	}
	if inspected.Name != name {
		return Inspection{}, compat.Fail(
			"E_OWNERSHIP_MISMATCH",
			"sbx inspect returned a different sandbox name",
		)
	}
	if !qualifiedState(p, inspected.State) {
		return Inspection{}, compat.Fail(
			"E_SBX_STATE_UNKNOWN",
			"unqualified sbx inspect state %q",
			inspected.State,
		)
	}
	if len(inspected.RuntimeMounts) != 0 {
		return Inspection{}, compat.Fail(
			"E_OWNERSHIP_MISMATCH",
			"owned sandbox unexpectedly contains runtime mounts",
		)
	}
	return inspected, nil
}

func inspectionFields(fields map[string]json.RawMessage) (Inspection, error) {
	var inspected Inspection
	if !validInspectionFields(fields, &inspected) {
		return Inspection{}, compat.Fail(
			"E_SBX_SCHEMA",
			"incomplete sbx inspect identity, state, image digest or runtime mounts",
		)
	}
	if id, present := fields["id"]; present &&
		(json.Unmarshal(id, &inspected.ID) != nil || inspected.ID == "") {
		return Inspection{}, compat.Fail("E_SBX_SCHEMA", "invalid sbx inspect ID")
	}
	return inspected, nil
}

func qualifiedState(p compat.Profile, state string) bool {
	kind, ok := p.InventoryStates[state]
	return ok && (kind == "running" || kind == "stopped" || kind == "transitional")
}

func validInspectionFields(fields map[string]json.RawMessage, inspected *Inspection) bool {
	return json.Unmarshal(fields["name"], &inspected.Name) == nil && inspected.Name != "" &&
		json.Unmarshal(fields["state"], &inspected.State) == nil && inspected.State != "" &&
		validInspectionImage(fields, inspected)
}

func validInspectionImage(fields map[string]json.RawMessage, inspected *Inspection) bool {
	return json.Unmarshal(fields["image_digest"], &inspected.ImageDigest) == nil &&
		inspected.ImageDigest != "" &&
		json.Unmarshal(fields["runtime_mounts"], &inspected.RuntimeMounts) == nil &&
		inspected.RuntimeMounts != nil
}
