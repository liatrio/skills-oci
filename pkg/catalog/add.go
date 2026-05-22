package catalog

// AddEntry appends e to c and returns a new Catalog value. The input
// catalog is never mutated; the returned slice does not share its
// backing array with c.Skills, so subsequent appends on either side are
// independent. Validation (including duplicate-name rejection) is
// delegated to Validate so add-time and load-time rules stay in lockstep.
func AddEntry(c Catalog, e Entry) (Catalog, error) {
	merged := make([]Entry, 0, len(c.Skills)+1)
	merged = append(merged, c.Skills...)
	merged = append(merged, e)
	out := Catalog{SchemaVersion: c.SchemaVersion, Skills: merged}
	if out.SchemaVersion == 0 {
		// Bootstrap convenience: AddEntry on a zero-value Catalog produces
		// a v1 catalog. Callers building from scratch don't have to set
		// SchemaVersion separately.
		out.SchemaVersion = 1
	}
	if err := Validate(out); err != nil {
		return Catalog{}, err
	}
	return out, nil
}
