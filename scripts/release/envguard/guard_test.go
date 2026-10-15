package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestViolations(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "literal release environment",
			yaml: "on: push\njobs:\n  a:\n    environment: release\n",
			want: []string{"job a uses the release environment"},
		},
		{
			name: "mapping with another case",
			yaml: "on: push\njobs:\n  a:\n    environment:\n      name: Release\n",
			want: []string{"job a uses the release environment"},
		},
		{
			name: "expression in a dispatched workflow",
			yaml: "on:\n  workflow_dispatch:\njobs:\n  a:\n    environment: ${{ inputs.env }}\n",
			want: []string{"job a takes its environment from an expression, and the workflow does not run only on workflow_call"},
		},
		{
			name: "expression in a reusable workflow",
			yaml: "on:\n  workflow_call:\njobs:\n  a:\n    environment:\n      name: ${{ inputs.environment }}\n",
		},
		{
			name: "release passed to a reusable workflow",
			yaml: "on: [push]\njobs:\n  a:\n    uses: ./.github/workflows/deploy.yaml\n    with:\n      environment: release\n      count: 3\n",
			want: []string{`job a passes "release" to input environment`},
		},
		{
			name: "other environments",
			yaml: "on: push\njobs:\n  a:\n    environment: Development\n  b:\n    uses: ./x.yml\n    with:\n      environment: Staging\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := violations([]byte(tt.yaml))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestViolationsRejectsInvalidYAML(t *testing.T) {
	_, err := violations([]byte("jobs: [\n"))
	require.ErrorContains(t, err, "yaml")
}

func TestRepositoryWorkflows(t *testing.T) {
	got, err := checkDir("../../../.github/workflows")
	require.NoError(t, err)
	require.Empty(t, got)
}
