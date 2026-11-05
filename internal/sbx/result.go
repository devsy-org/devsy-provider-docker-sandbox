package sbx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func stringFields(data []byte, names ...string) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, fmt.Errorf("expected JSON object")
	}
	fields := make(map[string]string, len(names))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid JSON object key")
		}
		known := false
		for _, name := range names {
			if key == name {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unexpected field %q", key)
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 || raw[0] != '"' {
			return nil, fmt.Errorf("field %q must be a string", key)
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, fmt.Errorf("invalid JSON object end")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("extra JSON after object")
	}
	if len(fields) != len(names) {
		return nil, fmt.Errorf("missing required fields")
	}
	return fields, nil
}

func CheckResult(data []byte, name, action string) error {
	if name == "" || (action != "stopped" && action != "removed") {
		return compat.Fail("E_SBX_SCHEMA", "invalid stop/remove result expectation")
	}
	var results []json.RawMessage
	if err := exact(data, &results); err != nil || len(results) != 1 {
		return compat.Fail("E_SBX_SCHEMA", "expected one stop/remove result for %q", name)
	}
	fields, err := stringFields(results[0], "action", "resource", "name")
	if err != nil || fields["action"] != action || fields["resource"] != "sandbox" || fields["name"] != name {
		return compat.Fail("E_SBX_SCHEMA", "unexpected stop/remove result for %q: %v", name, err)
	}
	return nil
}
