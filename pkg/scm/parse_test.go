package scm

import (
	"strings"
	"testing"
)

func TestParseGitHubTreeURL_HappyPaths(t *testing.T) {
	tests := []struct {
		name         string
		url          string
		wantOwner    string
		wantRepo     string
		wantRefOrSHA string
		wantSubpath  string
	}{
		{
			name:         "semver tag",
			url:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
			wantOwner:    "anthropics",
			wantRepo:     "skills",
			wantRefOrSHA: "v1.0.0",
			wantSubpath:  "skills/create-skill",
		},
		{
			name:         "branch name",
			url:          "https://github.com/anthropics/skills/tree/main/skills/create-skill",
			wantOwner:    "anthropics",
			wantRepo:     "skills",
			wantRefOrSHA: "main",
			wantSubpath:  "skills/create-skill",
		},
		{
			name:         "40-hex SHA",
			url:          "https://github.com/anthropics/skills/tree/bc6708cbbc37adb919157f04d31e601e68f4b9c2/skills/create-skill",
			wantOwner:    "anthropics",
			wantRepo:     "skills",
			wantRefOrSHA: "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
			wantSubpath:  "skills/create-skill",
		},
		{
			name:         "deep subpath",
			url:          "https://github.com/myorg/mono/tree/v2.3.1/a/b/c/d/skill",
			wantOwner:    "myorg",
			wantRepo:     "mono",
			wantRefOrSHA: "v2.3.1",
			wantSubpath:  "a/b/c/d/skill",
		},
		{
			name:         "trailing slash tolerated",
			url:          "https://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill/",
			wantOwner:    "anthropics",
			wantRepo:     "skills",
			wantRefOrSHA: "v1.0.0",
			wantSubpath:  "skills/create-skill",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, ref, subpath, err := ParseGitHubTreeURL(tt.url)
			if err != nil {
				t.Fatalf("ParseGitHubTreeURL(%q): %v", tt.url, err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
			if ref != tt.wantRefOrSHA {
				t.Errorf("refOrSHA = %q, want %q", ref, tt.wantRefOrSHA)
			}
			if subpath != tt.wantSubpath {
				t.Errorf("subpath = %q, want %q", subpath, tt.wantSubpath)
			}
		})
	}
}

func TestParseGitHubTreeURL_Rejections(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantInErr string
	}{
		{
			name:      "non-github host (gitlab)",
			url:       "https://gitlab.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
			wantInErr: "github.com",
		},
		{
			name:      "non-github host (bitbucket)",
			url:       "https://bitbucket.org/anthropics/skills/tree/v1.0.0/skills/create-skill",
			wantInErr: "github.com",
		},
		{
			name:      "http (not https)",
			url:       "http://github.com/anthropics/skills/tree/v1.0.0/skills/create-skill",
			wantInErr: "https",
		},
		{
			name:      "blob segment instead of tree",
			url:       "https://github.com/anthropics/skills/blob/v1.0.0/skills/create-skill/SKILL.md",
			wantInErr: "tree",
		},
		{
			name:      "releases segment instead of tree",
			url:       "https://github.com/anthropics/skills/releases/v1.0.0",
			wantInErr: "tree",
		},
		{
			name:      "missing subpath (just owner/repo/tree/ref)",
			url:       "https://github.com/anthropics/skills/tree/v1.0.0",
			wantInErr: "subpath",
		},
		{
			name:      "missing subpath with trailing slash",
			url:       "https://github.com/anthropics/skills/tree/v1.0.0/",
			wantInErr: "subpath",
		},
		{
			name:      "empty string",
			url:       "",
			wantInErr: "url",
		},
		{
			name:      "completely malformed",
			url:       "not-a-url",
			wantInErr: "https",
		},
		{
			name:      "only one path segment after host",
			url:       "https://github.com/anthropics",
			wantInErr: "owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, _, err := ParseGitHubTreeURL(tt.url)
			if err == nil {
				t.Fatalf("ParseGitHubTreeURL(%q) accepted, want error", tt.url)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q lacks %q", err.Error(), tt.wantInErr)
			}
		})
	}
}
