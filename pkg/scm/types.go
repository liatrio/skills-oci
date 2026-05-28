package scm

// SourceRef identifies an exact subpath inside an upstream Git repository
// at an immutable commit. Owner/Repo address the upstream; Subpath
// narrows to a directory; Commit pins the content. Callers building a
// SourceRef by hand are responsible for ensuring Commit is a 40-hex SHA
// (validation is enforced at the catalog layer and at the Fetch boundary).
type SourceRef struct {
	Owner   string
	Repo    string
	Subpath string
	Commit  string
}
