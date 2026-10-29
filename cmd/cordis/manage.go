package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type trustDocument struct {
	Keys map[string]struct {
		PublicKey string `json:"publicKey"`
		Publisher string `json:"publisher"`
		Revoked   bool   `json:"revoked"`
	} `json:"keys"`
	Publishers        map[string][]string        `json:"publishers"`
	RevokedPublishers map[string]bool            `json:"revokedPublishers"`
	PublisherChanges  map[string]map[string]bool `json:"publisherChanges"`
}

func loadTrust(file string) (packagehost.TrustPolicy, error) {
	doc, err := config.Load(file, "")
	if err != nil {
		return packagehost.TrustPolicy{}, fmt.Errorf("load trust: %w", err)
	}
	var input trustDocument
	if err := doc.Decode(&input); err != nil {
		return packagehost.TrustPolicy{}, fmt.Errorf("load trust: %w", err)
	}
	trust := packagehost.TrustPolicy{
		Keys: map[string]packagehost.TrustedKey{}, Publishers: input.Publishers,
		RevokedPublishers: input.RevokedPublishers, PublisherChanges: input.PublisherChanges,
	}
	for id, key := range input.Keys {
		public, err := hex.DecodeString(key.PublicKey)
		if err != nil || len(public) != ed25519.PublicKeySize || id == "" || key.Publisher == "" {
			return packagehost.TrustPolicy{}, fmt.Errorf("invalid Ed25519 public key or publisher for %q", id)
		}
		trust.Keys[id] = packagehost.TrustedKey{PublicKey: public, Publisher: key.Publisher, Revoked: key.Revoked}
	}
	return trust, nil
}

func parseRef(value string) (contract.PackageRef, error) {
	identity, digest, found := strings.Cut(value, "#")
	id, version, versionFound := strings.Cut(identity, "@")
	bytes, err := hex.DecodeString(digest)
	if !found || !versionFound || id == "" || err != nil || len(bytes) != 32 || strings.ToLower(digest) != digest || packagehost.ValidateVersion(version) != nil {
		return contract.PackageRef{}, &usageError{message: "package reference must be exact id@version#sha256"}
	}
	return contract.PackageRef{ID: id, Version: version, Digest: digest}, nil
}

type stringFlags []string

func (s *stringFlags) String() string { return strings.Join(*s, ",") }
func (s *stringFlags) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func enabledOptions(entries []contract.Entry) packagehost.ChangeOptions {
	var options packagehost.ChangeOptions
	for _, entry := range entries {
		if entry.Enabled && !slices.Contains(options.Enabled, entry.Package) {
			options.Enabled = append(options.Enabled, entry.Package)
		}
	}
	return options
}

func manageCommand(ctx context.Context, command string, flags *flag.FlagSet, parse func() (bool, error), in io.Reader, out, errOut io.Writer) (result error) {
	directory := flags.String("store", "", "explicit persistent store directory (must be idle)")
	needsTrust := command == "install" || command == "select" || command == "enable"
	trustFile := ""
	if needsTrust {
		flags.StringVar(&trustFile, "trust", "", "explicit JSON/TOML/YAML public-key trust file")
	}
	var packageID, definition, parent string
	var isolate stringFlags
	var configuration dataInput
	if command == "enable" {
		flags.StringVar(&packageID, "package", "", "selected package ID; required for a new entry")
		flags.StringVar(&definition, "definition", "", "definition; defaults to package default for new entries")
		flags.StringVar(&parent, "parent", "", "parent entry; explicit empty value clears it")
		flags.Var(&isolate, "isolate", "isolated full service key; repeat for multiple keys, empty clears")
		configuration.register(flags, "config")
	}
	if done, err := parse(); done {
		return err
	}
	count := flags.NArg()
	validCount := count == 1
	if command == "list" {
		validCount = count == 0
	} else if command == "status" {
		validCount = count <= 1
	}
	if *directory == "" || needsTrust && trustFile == "" || !validCount {
		return &usageError{message: commandUsage[command]}
	}
	var trust packagehost.TrustPolicy
	if needsTrust {
		var err error
		trust, err = loadTrust(trustFile)
		if err != nil {
			return err
		}
	}
	store, err := packagehost.OpenWithOptions(*directory, packagehost.Options{
		Trust: trust, Logger: slog.New(slog.NewJSONHandler(errOut, nil)),
	})
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, store.Close()) }()
	entries, err := store.Entries()
	if err != nil {
		return err
	}
	options := enabledOptions(entries)
	var value any
	switch command {
	case "install":
		pkg, err := store.Install(ctx, flags.Arg(0))
		if err != nil {
			return err
		}
		value = pkg.Ref
	case "select", "uninstall":
		ref, err := parseRef(flags.Arg(0))
		if err != nil {
			return err
		}
		if command == "select" {
			if err := store.Select(ctx, ref, options); err != nil {
				return err
			}
			value = ref
		} else {
			removed, err := store.Uninstall(ctx, ref, options)
			if err != nil {
				return err
			}
			value = removed
		}
	case "enable":
		seen := map[string]bool{}
		flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
		if err := configuration.check("config", seen); err != nil {
			return err
		}
		entry := contract.Entry{ID: flags.Arg(0)}
		for _, existing := range entries {
			if existing.ID == entry.ID {
				entry = existing
				break
			}
		}
		if seen["package"] {
			entry.Package = packageID
		}
		if entry.Package == "" {
			return &usageError{message: "new entry requires -package"}
		}
		if seen["definition"] {
			entry.Definition = definition
		}
		if seen["parent"] {
			entry.Parent = parent
		}
		if seen["isolate"] {
			if len(isolate) == 1 && isolate[0] == "" {
				isolate = nil
			}
			entry.Isolate = isolate
		}
		if seen["config"] || seen["config-file"] {
			entry.Config, err = configuration.load("config", in)
			if err != nil {
				return err
			}
		}
		saved, err := store.EnableEntry(ctx, entry)
		if err != nil {
			return err
		}
		value = saved
	case "disable", "remove":
		if command == "disable" {
			err = store.DisableEntry(ctx, flags.Arg(0))
		} else {
			err = store.DeleteEntry(ctx, flags.Arg(0))
		}
		if err != nil {
			return err
		}
		value = struct {
			ID        string `json:"id"`
			Operation string `json:"operation"`
		}{flags.Arg(0), command}
	case "list":
		installed, err := store.Installed()
		if err != nil {
			return err
		}
		type packageState struct {
			Ref      contract.PackageRef `json:"ref"`
			Selected bool                `json:"selected"`
		}
		packages := []packageState{}
		for _, pkg := range installed {
			selected, err := store.Selected(pkg.Ref.ID)
			if err != nil && !errors.Is(err, packagehost.ErrNotSelected) {
				return err
			}
			packages = append(packages, packageState{pkg.Ref, selected.Ref == pkg.Ref})
		}
		return writeJSON(out, struct {
			Offline  bool             `json:"offline"`
			Packages []packageState   `json:"packages"`
			Entries  []contract.Entry `json:"entries"`
		}{true, packages, entries})
	case "status":
		runs, err := store.Runs()
		if err != nil {
			return err
		}
		if id := flags.Arg(0); id != "" {
			entries = slices.DeleteFunc(entries, func(e contract.Entry) bool { return e.ID != id })
			runs = slices.DeleteFunc(runs, func(r packagehost.RunRecord) bool { return r.Owner.Entry != id })
			if len(entries) == 0 && len(runs) == 0 {
				return contract.Fail(contract.NotReady, "entry has no saved intent or recent runs")
			}
		}
		return writeJSON(out, struct {
			Offline bool                    `json:"offline"`
			Entries []contract.Entry        `json:"entries"`
			Runs    []packagehost.RunRecord `json:"runs"`
		}{true, entries, runs})
	}
	if err := writeJSON(out, value); err != nil {
		return fmt.Errorf("%s completed with persistent state committed; write result: %w", command, err)
	}
	return nil
}
