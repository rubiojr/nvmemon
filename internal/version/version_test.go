package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		injected string
		bi       *debug.BuildInfo
		ok       bool
		want     string
	}{
		{
			name:     "injected wins",
			injected: "v1.2.3",
			bi:       &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			ok:       true,
			want:     "v1.2.3",
		},
		{
			name: "module version from go install",
			bi:   &debug.BuildInfo{Main: debug.Module{Version: "v0.1.1"}},
			ok:   true,
			want: "v0.1.1",
		},
		{
			name: "devel falls back to vcs revision",
			bi: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abcdef1234567890"},
				},
			},
			ok:   true,
			want: "dev+abcdef123456",
		},
		{
			name: "dirty working tree",
			bi: &debug.BuildInfo{
				Main: debug.Module{Version: "(devel)"},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "abc123"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			ok:   true,
			want: "dev+abc123-dirty",
		},
		{
			name: "no build info",
			ok:   false,
			want: "dev",
		},
		{
			name: "devel without vcs info",
			bi:   &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			ok:   true,
			want: "dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolve(tt.injected, tt.bi, tt.ok))
		})
	}
}

func TestStringNonEmpty(t *testing.T) {
	assert.NotEmpty(t, String())
}
