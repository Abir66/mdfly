package localstate

import "sort"

// Ledger is an in-memory read-only view of the state.json records, with a
// derived path → slugs secondary index for file-oriented verb resolution.
type Ledger struct {
	records   map[string]Record
	pathIndex map[string][]string
}

func newLedger(records map[string]Record) *Ledger {
	if records == nil {
		records = map[string]Record{}
	}
	idx := map[string][]string{}
	for slug, rec := range records {
		if rec.Path != nil {
			idx[*rec.Path] = append(idx[*rec.Path], slug)
		}
	}
	for _, slugs := range idx {
		sort.Strings(slugs)
	}
	return &Ledger{records: records, pathIndex: idx}
}

// Get returns the record for slug. The returned Path is a copy, so callers
// cannot mutate ledger state or desync the path index.
func (l *Ledger) Get(slug string) (Record, bool) {
	rec, ok := l.records[slug]
	if !ok {
		return Record{}, false
	}
	return copyRecord(rec), true
}

// All returns every record sorted by slug, each with a copied Path.
func (l *Ledger) All() []Record {
	out := make([]Record, 0, len(l.records))
	for _, rec := range l.records {
		out = append(out, copyRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// copyRecord returns rec with an independent copy of its Path pointer.
func copyRecord(rec Record) Record {
	if rec.Path != nil {
		p := *rec.Path
		rec.Path = &p
	}
	return rec
}

// SlugsForPath returns the slugs published from an absolute path (0, 1, or
// many), sorted, for update/delete/open resolution.
func (l *Ledger) SlugsForPath(path string) []string {
	return append([]string(nil), l.pathIndex[path]...)
}
