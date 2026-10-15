package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	protectedEnvironment = "release"
	releaseWorkflow      = "release.yml"
)

type workflow struct {
	On   yaml.Node       `yaml:"on"`
	Jobs map[string]*job `yaml:"jobs"`
}

type job struct {
	Environment yaml.Node      `yaml:"environment"`
	Uses        string         `yaml:"uses"`
	With        map[string]any `yaml:"with"`
}

// checkDir returns the violations of every workflow file in dir except release.yml.
func checkDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == releaseWorkflow || (filepath.Ext(name) != ".yml" && filepath.Ext(name) != ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // names come from reading dir
		if err != nil {
			return nil, err
		}
		v, err := violations(data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		for _, s := range v {
			found = append(found, name+": "+s)
		}
	}
	return found, nil
}

// violations returns why the workflow in data could run a job in the release environment.
// An environment name from an expression is allowed only in a workflow that runs only on
// workflow_call, because its callers are checked for the literal value.
func violations(data []byte) ([]string, error) {
	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, err
	}
	onlyCalled := slices.Equal(triggers(&wf.On), []string{"workflow_call"})
	var found []string
	for name, j := range wf.Jobs {
		env := environmentName(&j.Environment)
		switch {
		case strings.EqualFold(env, protectedEnvironment):
			found = append(found, fmt.Sprintf("job %s uses the %s environment", name, protectedEnvironment))
		case strings.Contains(env, "${{") && !onlyCalled:
			found = append(found, fmt.Sprintf(
				"job %s takes its environment from an expression, and the workflow does not run only on workflow_call", name))
		}
		if j.Uses == "" {
			continue
		}
		for key, value := range j.With {
			if s, ok := value.(string); ok && strings.EqualFold(strings.TrimSpace(s), protectedEnvironment) {
				found = append(found, fmt.Sprintf("job %s passes %q to input %s", name, s, key))
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

func environmentName(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "name" {
				return n.Content[i+1].Value
			}
		}
	}
	return ""
}

func triggers(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		names := make([]string, len(n.Content))
		for i, c := range n.Content {
			names[i] = c.Value
		}
		return names
	case yaml.MappingNode:
		names := make([]string, 0, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			names = append(names, n.Content[i].Value)
		}
		return names
	}
	return nil
}
