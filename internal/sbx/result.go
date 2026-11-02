package sbx

import (
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
)

func CheckResult(data []byte, name string) error {
	return compat.Fail("E_SBX_SCHEMA", "successful stop/remove result schema has not been captured and qualified for exact target %q", name)
}
