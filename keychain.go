package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const (
	secretLogin = "login"
	secretCert  = "cert"
)

const securityBin = "/usr/bin/security"

func keychainAccount(id, kind string) string { return id + "/" + kind }

// Values are stored hex-encoded: `security -w` prints non-ASCII items as hex
// with no way to tell them from a literal hex password, and the encoding also
// removes any quoting concerns below.
func keychainSet(id, kind, secret string) error {
	// `security -i` reads the command from stdin, keeping the secret out of
	// the process list.
	cmd := exec.Command(securityBin, "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n",
		appID, keychainAccount(id, kind), hex.EncodeToString([]byte(secret))))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if !keychainHas(id, kind) {
		return errors.New("keychain: the item was not saved")
	}
	return nil
}

func keychainGet(id, kind string) (string, bool) {
	out, err := exec.Command(securityBin, "find-generic-password",
		"-s", appID, "-a", keychainAccount(id, kind), "-w").Output()
	if err != nil {
		return "", false
	}
	secret, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return "", false
	}
	return string(secret), true
}

// keychainHas never reads the value, so it can't trigger an access prompt.
func keychainHas(id, kind string) bool {
	return exec.Command(securityBin, "find-generic-password",
		"-s", appID, "-a", keychainAccount(id, kind)).Run() == nil
}

func keychainDelete(id, kind string) {
	// A missing item is the desired end state, so the error doesn't matter.
	_ = exec.Command(securityBin, "delete-generic-password",
		"-s", appID, "-a", keychainAccount(id, kind)).Run()
}
