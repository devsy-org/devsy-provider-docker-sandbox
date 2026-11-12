package main

import (
	"fmt"
	"path/filepath"

	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/compat"
	"github.com/devsy-org/devsy-provider-docker-sandbox/internal/qualify"
)

func fixtureFingerprint(client string) (string, error) {
	return qualify.VerifyCaptures(filepath.Join("internal", "sbx", "testdata", client))
}

func qualifiedCaptures(profile compat.Profile) error {
	for _, client := range profile.QualifiedSBXVersions {
		if _, err := fixtureFingerprint(client); err != nil {
			return fmt.Errorf("sbx %s captures: %w", client, err)
		}
	}
	return nil
}

func verifyFixtureEvidence(ev qualify.Evidence) error {
	for _, host := range ev.Hosts {
		fingerprint, err := fixtureFingerprint(host.ClientVersion)
		if err != nil {
			return fmt.Errorf("sbx %s captures: %w", host.ClientVersion, err)
		}
		if fingerprint != host.FixtureProvenanceSHA256 {
			return fmt.Errorf("%s: fixture provenance sha256 differs from evidence", host.Host)
		}
	}
	return nil
}
