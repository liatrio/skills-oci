package catalog

import (
	"strings"
	"testing"
	"time"
)

// validEntry returns a fully-valid v2 Entry that individual tests can
// mutate to exercise a single rejection rule. The default is a
// `published` row with a semver tag so semver validation runs.
func validEntry() Entry {
	return Entry{
		Namespace:     "liatrio",
		Name:          "create-skill",
		LatestVersion: "1.0.0",
		UpdatedAt:     time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		Status:        StatusPublished,
		Visibility:    VisibilityPublic,
		Repo:          "anthropics/skills",
		Subpath:       "skills/create-skill",
		Version:       "v1.0.0",
		Commit:        "bc6708cbbc37adb919157f04d31e601e68f4b9c2",
		InternalRef:   "ghcr.io/liatrio/skills/create-skill",
	}
}

// validCatalog returns a fully-valid v2 Catalog wrapping a single valid
// entry. Tests that exercise wrapper-level rules (schema_version,
// generated_at) mutate the returned value.
func validCatalog() Catalog {
	return Catalog{
		SchemaVersion: 2,
		GeneratedAt:   time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC),
		Skills:        []Entry{validEntry()},
	}
}

func TestValidate_AllValid(t *testing.T) {
	if err := Validate(validCatalog()); err != nil {
		t.Errorf("Validate rejected valid catalog: %v", err)
	}
}

func TestValidate_EmptyCatalogIsValid(t *testing.T) {
	c := validCatalog()
	c.Skills = nil
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected empty catalog: %v", err)
	}
}

func TestValidate_SchemaVersion(t *testing.T) {
	tests := []struct {
		name   string
		schema int
	}{
		{"zero", 0},
		{"one (legacy)", 1},
		{"three", 3},
		{"negative", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCatalog()
			c.SchemaVersion = tt.schema
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted schema_version=%d", tt.schema)
			}
			if !strings.Contains(err.Error(), "schema_version") {
				t.Errorf("error %q lacks 'schema_version' context", err.Error())
			}
		})
	}
}

func TestValidate_GeneratedAt_AcceptsZero(t *testing.T) {
	// The platform validator only checks the RFC3339 string format on
	// generated_at; a zero time (which serializes as
	// "0001-01-01T00:00:00Z") is acceptable. Mirror that tolerance here.
	c := validCatalog()
	c.GeneratedAt = time.Time{}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected zero generated_at: %v", err)
	}
}

func TestValidate_GeneratedAt_RequiresSecondPrecision(t *testing.T) {
	c := validCatalog()
	c.GeneratedAt = time.Date(2026, 5, 23, 12, 0, 0, 500_000_000, time.UTC)
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted sub-second generated_at")
	}
	if !strings.Contains(err.Error(), "generated_at") || !strings.Contains(err.Error(), "second precision") {
		t.Errorf("error %q lacks 'generated_at' and 'second precision' context", err.Error())
	}
}

func TestValidate_UpdatedAt_RequiresSecondPrecision(t *testing.T) {
	e := validEntry()
	e.UpdatedAt = time.Date(2026, 5, 23, 12, 0, 0, 1, time.UTC)
	c := validCatalog()
	c.Skills = []Entry{e}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted sub-second updated_at")
	}
	if !strings.Contains(err.Error(), "updated_at") || !strings.Contains(err.Error(), "second precision") {
		t.Errorf("error %q lacks 'updated_at' and 'second precision' context", err.Error())
	}
}

func TestValidate_GeneratedAt_RequiresUTC(t *testing.T) {
	c := validCatalog()
	loc, _ := time.LoadLocation("America/Chicago")
	c.GeneratedAt = time.Date(2026, 5, 23, 12, 0, 0, 0, loc)
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted non-UTC generated_at")
	}
	if !strings.Contains(err.Error(), "generated_at") {
		t.Errorf("error %q lacks 'generated_at' context", err.Error())
	}
}

func TestValidate_Namespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
	}{
		{"empty", ""},
		{"uppercase", "Liatrio"},
		{"underscore", "liatrio_labs"},
		{"trailing hyphen", "liatrio-"},
		{"double hyphen", "liatrio--labs"},
		{"slash", "liatrio/skills"},
		{"contains dot", "ghcr.io"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Namespace = tt.namespace
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted namespace=%q", tt.namespace)
			}
			if !strings.Contains(err.Error(), "namespace") {
				t.Errorf("error %q lacks 'namespace' context", err.Error())
			}
		})
	}
}

func TestValidate_LatestVersion_PublishedRequiresSemver(t *testing.T) {
	tests := []string{
		"",
		"v1.0.0", // leading-v rejected by SemVer 2.0.0
		"1.0",    // missing patch
		"latest",
		"abc",
	}
	for _, v := range tests {
		t.Run("latest_version="+v, func(t *testing.T) {
			e := validEntry()
			e.Status = StatusPublished
			e.LatestVersion = v
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted published latest_version=%q", v)
			}
			if !strings.Contains(err.Error(), "latest_version") {
				t.Errorf("error %q lacks 'latest_version' context", err.Error())
			}
		})
	}
}

func TestValidate_LatestVersion_UnversionedAcceptsEmpty(t *testing.T) {
	e := validEntry()
	e.Status = StatusUnversioned
	e.LatestVersion = ""
	c := validCatalog()
	c.Skills = []Entry{e}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected unversioned entry with empty latest_version: %v", err)
	}
}

func TestValidate_UpdatedAt_AcceptsZeroForUnpublished(t *testing.T) {
	// The indexer emits "0001-01-01T00:00:00Z" (the Go zero time) for
	// `status: unpublished` rows with no known activity timestamp; the
	// platform validator accepts it. Mirror that tolerance here.
	e := validEntry()
	e.Status = StatusUnpublished
	e.LatestVersion = ""
	e.UpdatedAt = time.Time{}
	c := validCatalog()
	c.Skills = []Entry{e}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected zero updated_at on unpublished row: %v", err)
	}
}

func TestValidate_Status(t *testing.T) {
	tests := []Status{"", "draft", "deleted", "PUBLISHED"}
	for _, s := range tests {
		t.Run("status="+string(s), func(t *testing.T) {
			e := validEntry()
			e.Status = s
			// Unhook the semver dependency so the test isolates the status rule.
			e.LatestVersion = ""
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted status=%q", s)
			}
			if !strings.Contains(err.Error(), "status") {
				t.Errorf("error %q lacks 'status' context", err.Error())
			}
		})
	}
}

func TestValidate_Visibility(t *testing.T) {
	tests := []Visibility{"", "private", "internal", "Public"}
	for _, v := range tests {
		t.Run("visibility="+string(v), func(t *testing.T) {
			e := validEntry()
			e.Visibility = v
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted visibility=%q", v)
			}
			if !strings.Contains(err.Error(), "visibility") {
				t.Errorf("error %q lacks 'visibility' context", err.Error())
			}
		})
	}
}

func TestValidate_Commit(t *testing.T) {
	tests := []struct {
		name   string
		commit string
	}{
		{"empty", ""},
		{"too short", "fff"},
		{"too long", "bc6708cbbc37adb919157f04d31e601e68f4b9c2a"},
		{"uppercase hex", "BC6708CBBC37ADB919157F04D31E601E68F4B9C2"},
		{"non-hex chars", "xyz0708cbbc37adb919157f04d31e601e68f4b9c2"},
		{"trailing space", "bc6708cbbc37adb919157f04d31e601e68f4b9c2 "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Commit = tt.commit
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted commit=%q", tt.commit)
			}
			if !strings.Contains(err.Error(), "commit") {
				t.Errorf("error %q lacks 'commit' context", err.Error())
			}
		})
	}
}

func TestValidate_Version_RejectsNonSemverNonSHA(t *testing.T) {
	// The `version` field accepts SemVer 2.0.0 (with optional leading `v`)
	// or a 40-hex SHA — nothing else. Branch names, mutable major-only
	// tags, calendar versions, and arbitrary strings all fail. This is a
	// strict allow-list, not a deny-list of known-bad values.
	tests := []string{
		"",                // empty
		"latest",          // moving label
		"main",            // branch
		"master",          // branch
		"HEAD",            // symbolic ref
		"v1",              // major-only (npm/GitHub Actions style — commonly retagged)
		"1",               // major-only without v
		"v1.2",            // major.minor
		"1.2",             // major.minor without v
		"garbage",         // arbitrary string
		"release-2026-01", // dated label
		"2026.01.15",      // CalVer
		"v1.2.3.4",        // four-segment
		"v1.2.3-",         // empty prerelease
		"v1.0.0+",         // empty build metadata
		"V1.0.0",          // capital V — SemVer requires lowercase
		"deadbeef",        // short hex (not 40 chars)
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef", // too long
		"BC6708CBBC37ADB919157F04D31E601E68F4B9C2",         // uppercase hex
	}
	for _, v := range tests {
		t.Run("version="+v, func(t *testing.T) {
			e := validEntry()
			e.Version = v
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted version=%q, want rejection", v)
			}
			if !strings.Contains(err.Error(), "version") {
				t.Errorf("error %q lacks 'version' context", err.Error())
			}
		})
	}
}

func TestValidate_Version_AcceptsSemverAndSHA(t *testing.T) {
	// Mirror image of the rejection test: every member of the allow-list
	// must validate cleanly. Covers the SemVer core, leading-v variants,
	// prerelease, build metadata, and 40-hex commit SHAs.
	tests := []string{
		"1.0.0",
		"v1.0.0",
		"0.0.0",
		"v0.0.1",
		"10.20.30",
		"1.0.0-alpha",
		"v1.0.0-rc.1",
		"1.0.0-0.3.7",
		"1.0.0+build.1",
		"v1.0.0+sha.abc1234",
		"1.0.0-rc.1+build.5",
		"bc6708cbbc37adb919157f04d31e601e68f4b9c2",
	}
	for _, v := range tests {
		t.Run("version="+v, func(t *testing.T) {
			e := validEntry()
			e.Version = v
			c := validCatalog()
			c.Skills = []Entry{e}
			if err := Validate(c); err != nil {
				t.Errorf("Validate rejected version=%q: %v", v, err)
			}
		})
	}
}

func TestValidate_Repo(t *testing.T) {
	tests := []struct {
		name string
		repo string
	}{
		{"empty", ""},
		{"with https scheme", "https://github.com/anthropics/skills"},
		{"with /tree/ path", "anthropics/skills/tree/main"},
		{"with /blob/ path", "anthropics/skills/blob/main"},
		{"missing slash", "anthropics"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Repo = tt.repo
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted repo=%q", tt.repo)
			}
			if !strings.Contains(err.Error(), "repo") {
				t.Errorf("error %q lacks 'repo' context", err.Error())
			}
		})
	}
}

func TestValidate_Subpath(t *testing.T) {
	tests := []struct {
		name    string
		subpath string
	}{
		{"empty", ""},
		{"leading slash", "/skills/create-skill"},
		{"backslash", "skills\\create-skill"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEntry()
			e.Subpath = tt.subpath
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted subpath=%q", tt.subpath)
			}
			if !strings.Contains(err.Error(), "subpath") {
				t.Errorf("error %q lacks 'subpath' context", err.Error())
			}
		})
	}
}

func TestValidate_Name(t *testing.T) {
	e := validEntry()
	e.Name = ""
	c := validCatalog()
	c.Skills = []Entry{e}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted empty name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error %q lacks 'name' context", err.Error())
	}
}

func TestValidate_Name_RejectsNonIdentifier(t *testing.T) {
	tests := []string{"Create-Skill", "create_skill", "create skill", "skills/foo"}
	for _, n := range tests {
		t.Run("name="+n, func(t *testing.T) {
			e := validEntry()
			e.Name = n
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted name=%q", n)
			}
			if !strings.Contains(err.Error(), "name") {
				t.Errorf("error %q lacks 'name' context", err.Error())
			}
		})
	}
}

func TestValidate_InternalRef(t *testing.T) {
	e := validEntry()
	e.InternalRef = ""
	c := validCatalog()
	c.Skills = []Entry{e}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted empty internal_ref")
	}
	if !strings.Contains(err.Error(), "internal_ref") {
		t.Errorf("error %q lacks 'internal_ref' context", err.Error())
	}
}

func TestValidate_IndexerManagedRowAccepted(t *testing.T) {
	// An indexer-managed row has all v2 surface fields but no source-pin
	// fields. The platform's discovery indexer writes rows like this for
	// artifacts it found in GHCR but didn't vendor. Validate must accept
	// them so a vendor-managed row can be appended without rewriting the
	// indexer's rows.
	indexerRow := Entry{
		Namespace:     "liatrio",
		Name:          "otel-instrumentation",
		LatestVersion: "1.0.0",
		UpdatedAt:     time.Date(2026, 5, 6, 19, 12, 59, 0, time.UTC),
		Status:        StatusPublished,
		Visibility:    VisibilityPublic,
		// no Repo/Subpath/Version/Commit/InternalRef
	}
	vendorRow := validEntry()
	vendorRow.Name = "create-skill"

	c := validCatalog()
	c.Skills = []Entry{indexerRow, vendorRow}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected mixed indexer+vendor catalog: %v", err)
	}
}

func TestValidate_PartialSourcePinRejected(t *testing.T) {
	// An entry with some source-pin fields and not others is malformed.
	// All-or-nothing: indexer rows have none; vendor rows have all five.
	cases := []struct {
		name   string
		mutate func(*Entry)
	}{
		{"only Repo", func(e *Entry) { e.Subpath = ""; e.Version = ""; e.Commit = ""; e.InternalRef = "" }},
		{"only InternalRef", func(e *Entry) { e.Repo = ""; e.Subpath = ""; e.Version = ""; e.Commit = "" }},
		{"missing Commit", func(e *Entry) { e.Commit = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := validEntry()
			tc.mutate(&e)
			c := validCatalog()
			c.Skills = []Entry{e}
			err := Validate(c)
			if err == nil {
				t.Fatalf("Validate accepted partial source-pin entry: %+v", e)
			}
			if !strings.Contains(err.Error(), "source-pin") {
				t.Errorf("error %q lacks 'source-pin' context", err.Error())
			}
		})
	}
}

func TestHasSourcePin(t *testing.T) {
	full := validEntry()
	if !HasSourcePin(full) {
		t.Error("HasSourcePin(full) = false, want true")
	}
	indexer := full
	indexer.Repo = ""
	indexer.Subpath = ""
	indexer.Version = ""
	indexer.Commit = ""
	indexer.InternalRef = ""
	if HasSourcePin(indexer) {
		t.Error("HasSourcePin(indexer-only) = true, want false")
	}
}

func TestValidate_RejectsDuplicateNamespaceName(t *testing.T) {
	// Duplicate (namespace, name) tuples are rejected.
	a := validEntry()
	b := validEntry()
	c := validCatalog()
	c.Skills = []Entry{a, b}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted duplicate (namespace, name)")
	}
	if !strings.Contains(err.Error(), "duplicate") || !strings.Contains(err.Error(), a.Name) {
		t.Errorf("error %q should mention 'duplicate' and the conflicting name", err.Error())
	}
}

func TestValidate_SameNameDifferentNamespaceAccepted(t *testing.T) {
	// The platform catalog legitimately holds rows like
	// (liatrio, otel-instrumentation) and (liatrio-labs, otel-instrumentation).
	// Same `name`, different `namespace` is not a duplicate.
	a := Entry{
		Namespace:  "liatrio",
		Name:       "otel-instrumentation",
		Status:     StatusUnpublished,
		Visibility: VisibilityPublic,
	}
	b := Entry{
		Namespace:  "liatrio-labs",
		Name:       "otel-instrumentation",
		Status:     StatusUnpublished,
		Visibility: VisibilityPublic,
	}
	c := validCatalog()
	c.Skills = []Entry{a, b}
	if err := Validate(c); err != nil {
		t.Errorf("Validate rejected same-name-different-namespace catalog: %v", err)
	}
}

func TestValidate_ErrorIncludesEntryIndex(t *testing.T) {
	// When validation fails on the Nth entry, the error should help the
	// caller find that entry. Index in the message keeps the error usable
	// even when entries share a name (and validation hasn't reached the
	// duplicate-name check yet).
	a := validEntry()
	a.Name = "first"
	b := validEntry()
	b.Name = "second"
	b.Commit = "not-a-sha"
	c := validCatalog()
	c.Skills = []Entry{a, b}
	err := Validate(c)
	if err == nil {
		t.Fatal("Validate accepted bad commit on second entry")
	}
	if !strings.Contains(err.Error(), "second") {
		t.Errorf("error %q should mention the failing entry by name", err.Error())
	}
}
