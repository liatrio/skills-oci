package oci

import "testing"

func TestBuildOCIRef(t *testing.T) {
	cases := []struct {
		name       string
		registry   string
		repository string
		tag        string
		want       string
	}{
		{
			name:       "tagged ref uses colon joiner",
			registry:   "ghcr.io",
			repository: "liatrio-labs/skills/example",
			tag:        "1.0.0",
			want:       "ghcr.io/liatrio-labs/skills/example:1.0.0",
		},
		{
			name:       "sha256 digest uses at joiner",
			registry:   "ghcr.io",
			repository: "liatrio-labs/skills/example",
			tag:        "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
			want:       "ghcr.io/liatrio-labs/skills/example@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd",
		},
		{
			name:       "sha512 digest uses at joiner",
			registry:   "ghcr.io",
			repository: "liatrio-labs/skills/example",
			tag:        "sha512:0123456789abcdef",
			want:       "ghcr.io/liatrio-labs/skills/example@sha512:0123456789abcdef",
		},
		{
			name:       "registry with port keeps colon",
			registry:   "localhost:5000",
			repository: "skills/example",
			tag:        "1.0.0",
			want:       "localhost:5000/skills/example:1.0.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildOCIRef(tc.registry, tc.repository, tc.tag)
			if got != tc.want {
				t.Errorf("buildOCIRef(%q,%q,%q) = %q, want %q",
					tc.registry, tc.repository, tc.tag, got, tc.want)
			}
		})
	}
}
