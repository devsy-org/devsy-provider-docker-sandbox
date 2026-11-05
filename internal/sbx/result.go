package sbx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func stringFields(data []byte, names ...string) (map[string]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("expected JSON object")
	}
	fields := make(map[string]string, len(names))
	for decoder.More() {
		key, value, err := readStringField(decoder, names, fields)
		if err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if err := finishStringFields(decoder, fields, names); err != nil {
		return nil, err
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
	if err != nil || !matchesResult(fields, name, action) {
		return compat.Fail("E_SBX_SCHEMA", "unexpected stop/remove result for %q: %v", name, err)
	}
	return nil
}

func readStringField(
	decoder *json.Decoder,
	names []string,
	fields map[string]string,
) (keyResult string, valueResult string, resultErr error) {
	token, err := decoder.Token()
	if err != nil {
		return "", "", err
	}
	key, ok := token.(string)
	if !ok {
		return "", "", errors.New("invalid JSON object key")
	}
	known := slices.Contains(names, key)
	if !known {
		return "", "", fmt.Errorf("unexpected field %q", key)
	}
	if _, exists := fields[key]; exists {
		return "", "", fmt.Errorf("duplicate field %q", key)
	}
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return "", "", err
	}
	if !isJSONString(raw) {
		return "", "", fmt.Errorf("field %q must be a string", key)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", "", err
	}
	return key, value, nil
}

func matchesResult(fields map[string]string, name, action string) bool {
	return fields["action"] == action && fields["resource"] == "sandbox" && fields["name"] == name
}

func finishStringFields(decoder *json.Decoder, fields map[string]string, names []string) error {
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("invalid JSON object end")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("extra JSON after object")
	}
	if len(fields) != len(names) {
		return errors.New("missing required fields")
	}
	return nil
}

func isJSONString(raw json.RawMessage) bool { return len(raw) > 0 && raw[0] == '"' }
