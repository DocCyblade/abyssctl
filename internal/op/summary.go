package op

// Row is one fact in a Summary, e.g. {"from", "26.10.0"}.
type Row struct{ Label, Value string }

// Summary is a plan: what a changing operation would do. The CLI renders it as
// the summary panel. Its values are never Secrets.
type Summary struct {
	Title    string   // e.g. "Upgrade kids"
	Rows     []Row    // facts in order, e.g. {"from", "26.10.0"}, {"to", "26.11.0"}, {"downtime", "about 1 min"}
	Steps    []string // what will happen, in order, one short line each
	Warnings []string // e.g. "the key file is deleted; keep a copy if you need it"
	NoOp     bool     // nothing to do
}
