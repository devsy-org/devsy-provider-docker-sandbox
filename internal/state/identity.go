package state

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/config"
)

func Token() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func Name(key, generation string) string { return "devsy-" + key[:12] + "-" + generation }
func Root() (string, error) {
	base, err := compatRoot()
	if err != nil {
		return "", err
	}
	return base, nil
}
func New(identity config.MachineIdentity, spec config.Spec) (Record, error) {
	token, err := Token()
	if err != nil {
		return Record{}, err
	}
	op, err := Token()
	if err != nil {
		return Record{}, err
	}
	now := nowUTC()
	return Record{SchemaVersion: 1, MachineKey: identity.Key, MachineFolder: identity.Folder, MachineID: identity.ID, Context: identity.Context, ProviderFamily: config.Family, ProviderAlias: identity.Provider, SandboxName: Name(identity.Key, token), Generation: token, Epoch: 1, Phase: "creating", DesiredState: "running", Operation: &Operation{ID: op, Kind: "create", Phase: "active", Step: "create"}, Spec: spec, CreatedAt: now, UpdatedAt: now}, nil
}
