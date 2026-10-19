package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	protectedEnvironment = "release"
	releaseWorkflow      = "release.yml"
	localWorkflowPrefix  = "./.github/workflows/"
)

var (
	inputExpression = regexp.MustCompile(`^\$\{\{\s*inputs\.([A-Za-z0-9_-]+)\s*\}\}$`)
	// A called workflow runs with the environments of this repository, so only reviewed code may be
	// called: workflows of this repository at the same commit, or a fixed commit of the shared workflows.
	allowedExternal = regexp.MustCompile(`^NethermindEth/github-workflows/\.github/workflows/[^@]+@[0-9a-f]{40}$`)
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

// checkDir returns the violations of the workflow files in dir.
func checkDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make(map[string][]byte)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || (filepath.Ext(name) != ".yml" && filepath.Ext(name) != ".yaml") {
			continue
		}
		if files[name], err = os.ReadFile(filepath.Join(dir, name)); err != nil { //nolint:gosec // names come from reading dir
			return nil, err
		}
	}
	return checkFiles(files)
}

// checkFiles returns why a workflow other than release.yml could run a job in the release environment.
// A workflow that runs only on workflow_call may take its environment from one of its inputs. Its
// callers must then pass a literal value to that input.
func checkFiles(files map[string][]byte) ([]string, error) {
	workflows := make(map[string]*workflow, len(files))
	for name, data := range files {
		if name == releaseWorkflow {
			continue
		}
		wf := new(workflow)
		if err := yaml.Unmarshal(data, wf); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		workflows[name] = wf
	}

	var found violations
	envInputs := make(map[string]map[string]bool) // workflow file -> lower-case inputs that set an environment
	for file, wf := range workflows {
		found.checkEnvironments(file, wf, envInputs)
	}
	for file, wf := range workflows {
		found.checkCalls(file, wf, envInputs)
	}
	sort.Strings(found)
	return found, nil
}

type violations []string

func (v *violations) addf(file, format string, args ...any) {
	*v = append(*v, file+": "+fmt.Sprintf(format, args...))
}

// checkEnvironments checks the environments of the jobs in wf, and records in envInputs the inputs
// that a reusable workflow takes an environment from.
func (v *violations) checkEnvironments(file string, wf *workflow, envInputs map[string]map[string]bool) {
	onlyCalled := slices.Equal(triggers(&wf.On), []string{"workflow_call"})
	for name, j := range wf.Jobs {
		env, ok := environmentName(&j.Environment)
		switch {
		case !ok:
			v.addf(file, "job %s has an environment that envguard cannot read", name)
		case strings.EqualFold(strings.TrimSpace(env), protectedEnvironment):
			v.addf(file, "job %s uses the %s environment", name, protectedEnvironment)
		case !strings.Contains(env, "${{"):
		case !onlyCalled:
			v.addf(file, "job %s takes its environment from an expression, and the workflow does not run only on workflow_call", name)
		default:
			m := inputExpression.FindStringSubmatch(env)
			if m == nil {
				v.addf(file, "job %s takes its environment from %q, and a reusable workflow may take it only from ${{ inputs.<name> }}", name, env)
				continue
			}
			if envInputs[file] == nil {
				envInputs[file] = make(map[string]bool)
			}
			envInputs[file][strings.ToLower(m[1])] = true
		}
	}
}

// checkCalls checks the reusable workflows that the jobs in wf call, and the inputs they pass.
func (v *violations) checkCalls(file string, wf *workflow, envInputs map[string]map[string]bool) {
	for name, j := range wf.Jobs {
		if j.Uses == "" {
			continue
		}
		callee, local := strings.CutPrefix(j.Uses, localWorkflowPrefix)
		if !local && !allowedExternal.MatchString(j.Uses) {
			v.addf(file, "job %s calls %s, which is not a workflow of this repository or a commit of NethermindEth/github-workflows", name, j.Uses)
		}
		for key, value := range j.With {
			s, isString := value.(string)
			switch {
			case isString && strings.EqualFold(strings.TrimSpace(s), protectedEnvironment):
				v.addf(file, "job %s passes %q to input %s", name, s, key)
			case envInputs[callee][strings.ToLower(key)] && (!isString || strings.Contains(s, "${{")):
				v.addf(file, "job %s passes an expression to input %s of %s, which sets the environment", name, key, callee)
			}
		}
	}
}

// environmentName returns the environment name of a job, following YAML aliases. ok is false for a
// form that envguard cannot read, such as a merge key.
func environmentName(n *yaml.Node) (name string, ok bool) {
	n = resolve(n)
	switch n.Kind {
	case 0: // no environment
		return "", true
	case yaml.ScalarNode:
		return n.Value, true
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			switch resolve(n.Content[i]).Value {
			case "<<":
				return "", false
			case "name":
				v := resolve(n.Content[i+1])
				if v.Kind != yaml.ScalarNode {
					return "", false
				}
				name = v.Value
			}
		}
		return name, true
	}
	return "", false
}

func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
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
