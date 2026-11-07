// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"abarow.com/services/update/internal/channel"
	"abarow.com/services/update/internal/policy"
	"abarow.com/services/update/internal/token"
)

// Channel subcommands (used by the publisher's promotion workflow):
//
//	server sign-channel <role> <key-file> <manifest.json>   signs the manifest, prints the signed JSON
//	server countersign-channel <role> <key-file> <signed.json>      adds a second signature to an existing signed manifest
//	server verify-channel <trust.json> <level> <signed.json>      verifies the manifest from the point of view of a level
//	server generate-policy <level> <mirror> <trust.json> <dir>   writes the image signature check (policy.json, registries.d)
//	server check-trust <trust.json>                                lists today's state of the keys in the trust file (valid, not yet valid, expired)
//
// Role: build, pilot or prod. The key file is the output of "server generate-key".
// isChannelCommand says whether it is one of the channel subcommands.
func isChannelCommand(name string) bool {
	switch name {
	case "sign-channel", "countersign-channel", "verify-channel", "generate-policy", "check-trust":
		return true
	}
	return false
}

func channelCommand(args []string) error {
	switch args[0] {
	case "sign-channel":
		if len(args) != 4 {
			return errors.New("usage: server sign-channel <role> <key-file> <manifest.json>")
		}
		role, key, err := roleKey(args[1], args[2])
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		var m channel.Manifest
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			return fmt.Errorf("could not read the manifest: %w", err)
		}
		s, err := channel.Sign(m, role, key)
		if err != nil {
			return err
		}
		return printJSON(s)
	case "countersign-channel":
		if len(args) != 4 {
			return errors.New("usage: server countersign-channel <role> <key-file> <signed.json>")
		}
		role, key, err := roleKey(args[1], args[2])
		if err != nil {
			return err
		}
		s, err := readSigned(args[3])
		if err != nil {
			return err
		}
		return printJSON(s.Add(role, key))
	case "generate-policy":
		if len(args) != 5 {
			return errors.New("usage: server generate-policy <level> <mirror> <trust.json> <dir>")
		}
		raw, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		keys, err := policy.ParseKeys(raw, time.Now())
		if err != nil {
			return err
		}
		files, err := policy.Generate(token.Level(args[1]), args[2], keys)
		if err != nil {
			return err
		}
		changed, err := policy.Install(args[4], files)
		if err != nil {
			return err
		}
		fmt.Printf("policy written (%s, changed: %v)\n", args[4], changed)
		return nil
	case "check-trust":
		if len(args) != 2 {
			return errors.New("usage: server check-trust <trust.json>")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		now := time.Now()
		infos, err := channel.Inspect(raw, now)
		if err != nil {
			return err
		}
		valid := map[string]int{}
		for _, k := range infos {
			until := "no expiry"
			if !k.NotAfter.IsZero() {
				until = k.NotAfter.UTC().Format("2006-01-02")
			}
			from := "from the beginning"
			if !k.NotBefore.IsZero() {
				from = k.NotBefore.UTC().Format("2006-01-02")
			}
			fmt.Printf("%-8s %-6s %-12s %-20s %s .. %s\n", k.Kind, k.Role, k.ID, k.State, from, until)
			if k.State == channel.KeyValid {
				valid[k.Kind+"/"+string(k.Role)]++
			}
		}
		var missing []string
		for _, kind := range []string{"manifest", "cosign"} {
			for _, r := range []channel.Role{channel.Build, channel.Pilot, channel.Prod} {
				if kind == "cosign" && r == channel.Build {
					continue
				}
				if valid[kind+"/"+string(r)] == 0 {
					missing = append(missing, kind+"/"+string(r))
				}
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("roles with no key valid today: %s", strings.Join(missing, ", "))
		}
		return nil
	case "verify-channel":
		if len(args) != 4 {
			return errors.New("usage: server verify-channel <trust.json> <level> <signed.json>")
		}
		raw, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		trust, err := channel.ParseTrust(raw)
		if err != nil {
			return err
		}
		s, err := readSigned(args[3])
		if err != nil {
			return err
		}
		m, err := channel.Verify(s, token.Level(args[2]), trust, time.Now(), 0)
		if err != nil {
			return err
		}
		fmt.Printf("valid: channel=%s version=%s seq=%d urgency=%s images=%d expires=%s\n", m.Channel, m.Version, m.Seq, m.Urgency, len(m.Images), m.Expires.Format(time.RFC3339))
		return nil
	}
	return errors.New("bilinmeyen komut")
}

func roleKey(role, file string) (channel.Role, ed25519.PrivateKey, error) {
	switch r := channel.Role(role); r {
	case channel.Build, channel.Pilot, channel.Prod:
		key, err := readSeed(file)
		return r, key, err
	}
	return "", nil, fmt.Errorf("role must be build, pilot or prod: %q", role)
}

func readSigned(path string) (channel.Signed, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return channel.Signed{}, err
	}
	var s channel.Signed
	if err := json.Unmarshal(raw, &s); err != nil {
		return channel.Signed{}, fmt.Errorf("could not read the signed manifest: %w", err)
	}
	return s, nil
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
