package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const deployYAML = "on:\n  workflow_call:\n    inputs:\n      environment:\n        type: string\njobs:\n  d:\n    environment:\n      name: ${{ inputs.environment }}\n"

func TestCheckFiles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "literal release environment",
			files: map[string]string{"w.yml": "on: push\njobs:\n  a:\n    environment: release\n"},
			want:  []string{"w.yml: job a uses the release environment"},
		},
		{
			name:  "mapping with another case",
			files: map[string]string{"w.yml": "on: push\njobs:\n  a:\n    environment:\n      name: Release\n"},
			want:  []string{"w.yml: job a uses the release environment"},
		},
		{
			name:  "alias",
			files: map[string]string{"w.yml": "x: &rel release\non: push\njobs:\n  a:\n    environment: *rel\n"},
			want:  []string{"w.yml: job a uses the release environment"},
		},
		{
			name:  "alias as the name",
			files: map[string]string{"w.yml": "x: &rel release\non: push\njobs:\n  a:\n    environment:\n      name: *rel\n"},
			want:  []string{"w.yml: job a uses the release environment"},
		},
		{
			name:  "merge key",
			files: map[string]string{"w.yml": "x: &env\n  name: release\non: push\njobs:\n  a:\n    environment:\n      <<: *env\n"},
			want:  []string{"w.yml: job a has an environment that envguard cannot read"},
		},
		{
			name:  "sequence",
			files: map[string]string{"w.yml": "on: push\njobs:\n  a:\n    environment: [release]\n"},
			want:  []string{"w.yml: job a has an environment that envguard cannot read"},
		},
		{
			name:  "expression in a dispatched workflow",
			files: map[string]string{"w.yml": "on:\n  workflow_dispatch:\njobs:\n  a:\n    environment: ${{ inputs.env }}\n"},
			want:  []string{"w.yml: job a takes its environment from an expression, and the workflow does not run only on workflow_call"},
		},
		{
			name:  "input in a reusable workflow",
			files: map[string]string{"deploy.yml": deployYAML},
		},
		{
			name:  "other expression in a reusable workflow",
			files: map[string]string{"deploy.yml": "on: workflow_call\njobs:\n  d:\n    environment: ${{ inputs.e || 'release' }}\n"},
			want:  []string{`deploy.yml: job d takes its environment from "${{ inputs.e || 'release' }}", and a reusable workflow may take it only from ${{ inputs.<name> }}`},
		},
		{
			name: "release passed to a reusable workflow",
			files: map[string]string{
				"w.yml": "on: [push]\njobs:\n  a:\n    uses: ./.github/workflows/build.yml\n    with:\n      environment: release\n      count: 3\n",
			},
			want: []string{`w.yml: job a passes "release" to input environment`},
		},
		{
			name: "expression passed as the environment of a reusable workflow",
			files: map[string]string{
				"deploy.yml": deployYAML,
				"w.yml":      "on: push\njobs:\n  a:\n    uses: ./.github/workflows/deploy.yml\n    with:\n      Environment: ${{ 'release' }}\n",
			},
			want: []string{"w.yml: job a passes an expression to input Environment of deploy.yml, which sets the environment"},
		},
		{
			name: "literal environment and other expressions passed to a reusable workflow",
			files: map[string]string{
				"deploy.yml": deployYAML,
				"w.yml":      "on: push\njobs:\n  a:\n    uses: ./.github/workflows/deploy.yml\n    with:\n      environment: Development\n      tag: ${{ github.sha }}\n",
			},
		},
		{
			name: "reusable workflow of another repository",
			files: map[string]string{
				"w.yml": "on: push\njobs:\n  a:\n    uses: someone/repo/.github/workflows/x.yml@0123456789abcdef0123456789abcdef01234567\n",
			},
			want: []string{"w.yml: job a calls someone/repo/.github/workflows/x.yml@0123456789abcdef0123456789abcdef01234567, which is not a workflow of this repository or a commit of NethermindEth/github-workflows"},
		},
		{
			name: "this repository at another ref",
			files: map[string]string{
				"w.yml": "on: push\njobs:\n  a:\n    uses: NethermindEth/juno/.github/workflows/release.yml@main\n",
			},
			want: []string{"w.yml: job a calls NethermindEth/juno/.github/workflows/release.yml@main, which is not a workflow of this repository or a commit of NethermindEth/github-workflows"},
		},
		{
			name: "NethermindEth/github-workflows at a tag",
			files: map[string]string{
				"w.yml": "on: push\njobs:\n  a:\n    uses: NethermindEth/github-workflows/.github/workflows/ai-review.yaml@v2.5.0\n",
			},
			want: []string{"w.yml: job a calls NethermindEth/github-workflows/.github/workflows/ai-review.yaml@v2.5.0, which is not a workflow of this repository or a commit of NethermindEth/github-workflows"},
		},
		{
			name: "NethermindEth/github-workflows at a commit",
			files: map[string]string{
				"w.yml": "on: push\njobs:\n  a:\n    uses: NethermindEth/github-workflows/.github/workflows/ai-review.yaml@e0fad164abb5ef88a6aa54b21e84996fa62b5e19\n",
			},
		},
		{
			name:  "other environments",
			files: map[string]string{"w.yml": "on: push\njobs:\n  a:\n    environment: Development\n  b:\n    uses: ./.github/workflows/x.yml\n    with:\n      environment: Staging\n"},
		},
		{
			name:  "release.yml is not checked",
			files: map[string]string{"release.yml": "on: workflow_dispatch\njobs:\n  p:\n    environment: release\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := make(map[string][]byte, len(tt.files))
			for name, data := range tt.files {
				files[name] = []byte(data)
			}
			got, err := checkFiles(files)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCheckFilesRejectsInvalidYAML(t *testing.T) {
	_, err := checkFiles(map[string][]byte{"w.yml": []byte("jobs: [\n")})
	require.ErrorContains(t, err, "parsing w.yml")
}

func TestRepositoryWorkflows(t *testing.T) {
	got, err := checkDir("../../../.github/workflows")
	require.NoError(t, err)
	require.Empty(t, got)
}
