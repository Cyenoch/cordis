package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type devPackage struct {
	directory string
	goPackage string
	id        string
}

type devPlan struct {
	packages []devPackage
	entries  []contract.Entry
	roots    []string
	explicit map[string]bool
	outputs  map[string]bool
	digest   string
}

type devProject struct {
	Packages []struct {
		Directory string `json:"directory"`
		GoPackage string `json:"goPackage"`
	} `json:"packages"`
	Entries []struct {
		ID         string          `json:"id"`
		Package    string          `json:"package"`
		Definition string          `json:"definition,omitempty"`
		Parent     string          `json:"parent,omitempty"`
		Isolate    []string        `json:"isolate,omitempty"`
		Config     json.RawMessage `json:"config,omitempty"`
		ConfigFile string          `json:"configFile,omitempty"`
	} `json:"entries"`
	Watch []string `json:"watch,omitempty"`
}

func readDevProject(file string) (project devProject, raw []byte, result error) {
	input, err := os.Open(file)
	if err != nil {
		return project, nil, err
	}
	defer func() { result = errors.Join(result, input.Close()) }()
	raw, err = io.ReadAll(io.LimitReader(input, (1<<20)+1))
	if err != nil {
		return project, nil, err
	}
	document, err := config.Parse(file, raw, "")
	if err != nil {
		return project, nil, err
	}
	if err := document.Decode(&project); err != nil {
		return project, nil, err
	}
	if len(project.Packages) == 0 || len(project.Entries) == 0 {
		return project, nil, fmt.Errorf("dev project requires local packages and entries")
	}
	return project, raw, nil
}

// plan freezes parsed configuration before its digest is combined with the
// filesystem scan. A later scan must never label an older plan as current.
func (o devOptions) plan() (*devPlan, error) {
	plan := &devPlan{explicit: map[string]bool{}, outputs: map[string]bool{}}
	inputHash := sha256.New()
	var project devProject
	base := ""
	if o.project != "" {
		var raw []byte
		var err error
		project, raw, err = readDevProject(o.project)
		if err != nil {
			return nil, err
		}
		base = filepath.Dir(o.project)
		fmt.Fprintf(inputHash, "%d:", len(raw))
		inputHash.Write(raw)
		plan.explicit[o.project] = true
	} else {
		project.Packages = append(project.Packages, struct {
			Directory string `json:"directory"`
			GoPackage string `json:"goPackage"`
		}{o.source, o.goPackage})
	}
	resolve := func(name string) (string, error) {
		if name == "" {
			return "", fmt.Errorf("dev project contains an empty path")
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(base, name)
		}
		return filepath.Abs(name)
	}
	ids, directories := map[string]bool{}, map[string]bool{}
	for _, item := range project.Packages {
		if item.GoPackage == "" {
			return nil, fmt.Errorf("dev package %q requires goPackage", item.Directory)
		}
		directory, err := resolve(item.Directory)
		if err == nil {
			directory, err = filepath.EvalSymlinks(directory)
		}
		if err != nil {
			return nil, err
		}
		author, err := packagehost.LoadAuthor(directory)
		if err != nil {
			return nil, err
		}
		targets := author.Manifest.Runtime.Artifacts
		if len(targets) != 1 || targets[0].OS != runtime.GOOS || targets[0].Arch != runtime.GOARCH {
			return nil, fmt.Errorf("dev requires exactly one native target matching %s/%s: %s", runtime.GOOS, runtime.GOARCH, directory)
		}
		if ids[author.Manifest.ID] || directories[directory] {
			return nil, fmt.Errorf("duplicate dev package or directory: %s (%s)", author.Manifest.ID, directory)
		}
		ids[author.Manifest.ID], directories[directory] = true, true
		plan.packages = append(plan.packages, devPackage{directory, item.GoPackage, author.Manifest.ID})
		module, err := nearestModule(directory)
		if err != nil {
			return nil, err
		}
		plan.roots = append(plan.roots, module)
		for _, name := range append(author.Inputs(), author.Manifest.Resources...) {
			plan.explicit[filepath.Join(directory, filepath.FromSlash(name))] = true
		}
		for _, name := range []string{"cordis_gen.go", packagehost.BuildDescriptorFile, targets[0].Path} {
			path := filepath.Join(directory, filepath.FromSlash(name))
			plan.outputs[path] = true
			delete(plan.explicit, path)
		}
	}
	for _, name := range project.Watch {
		path, err := resolve(name)
		if err != nil {
			return nil, err
		}
		plan.roots = append(plan.roots, path)
	}
	// Command-line watch paths retain their normal working-directory meaning.
	for _, name := range o.extra {
		path, err := filepath.Abs(name)
		if err != nil {
			return nil, err
		}
		plan.roots = append(plan.roots, path)
	}
	var configErr error
	entryIDs := map[string]bool{}
	if o.project == "" {
		value, err := o.configuration.load("config", nil)
		configErr = err
		plan.entries = []contract.Entry{{ID: "dev", Package: plan.packages[0].id, Definition: o.definition, Config: value, Enabled: true}}
		if o.configuration.file != "" {
			path, err := filepath.Abs(o.configuration.file)
			if err != nil {
				return nil, err
			}
			plan.explicit[path] = true
		}
	} else {
		for _, item := range project.Entries {
			if item.ID == "" || entryIDs[item.ID] || !ids[item.Package] {
				return nil, fmt.Errorf("invalid or duplicate entry %q or unknown package %q", item.ID, item.Package)
			}
			if item.ConfigFile != "" && len(item.Config) != 0 {
				return nil, fmt.Errorf("entry %s: config and configFile are mutually exclusive", item.ID)
			}
			entryIDs[item.ID] = true
			value := item.Config
			if item.ConfigFile != "" {
				path, err := resolve(item.ConfigFile)
				if err != nil {
					return nil, err
				}
				plan.explicit[path] = true
				document, err := config.Load(path, "")
				configErr = errors.Join(configErr, err)
				if err == nil {
					value = document.JSON
				}
			}
			if len(value) == 0 {
				value = json.RawMessage(`{}`)
			}
			if err := contract.CheckIsolate(item.Isolate); err != nil {
				return nil, err
			}
			plan.entries = append(plan.entries, contract.Entry{
				ID: item.ID, Package: item.Package, Definition: item.Definition,
				Parent: item.Parent, Isolate: item.Isolate, Config: value, Enabled: true,
			})
		}
	}
	if err := contract.CheckEntryGraph(plan.entries); err != nil {
		return nil, err
	}
	values, err := json.Marshal(plan.entries)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(inputHash, "%d:", len(values))
	inputHash.Write(values)
	plan.digest = hex.EncodeToString(inputHash.Sum(nil))
	slices.Sort(plan.roots)
	plan.roots = slices.Compact(plan.roots)
	slices.SortFunc(plan.packages, func(a, b devPackage) int { return strings.Compare(a.id, b.id) })
	return plan, configErr
}

func nearestModule(source string) (string, error) {
	for dir := source; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if filepath.Dir(dir) == dir {
			return source, nil
		}
	}
}
