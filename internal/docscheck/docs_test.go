// Package docscheck holds the repo's docs tests. It has test files only and imports only the standard library.
package docscheck

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// repoRoot walks up from the test's working folder to the folder that holds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working folder")
		}
		dir = parent
	}
}

// skippedDirs are never searched for Markdown files.
var skippedDirs = map[string]bool{".git": true, "bin": true, "dist": true, "testdata": true}

// markdownFiles lists every *.md file under root, outside the skipped folders and outside any nested checkout
// (a folder with its own .git, such as a worktree).
func markdownFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

var fenceRE = regexp.MustCompile("^\\s*(`{3,}|~{3,})")

// proseLines returns the file's lines with fenced code blocks blanked, so line numbers stay true.
func proseLines(text string) []string {
	lines := strings.Split(text, "\n")
	fence := ""
	for i, line := range lines {
		m := fenceRE.FindStringSubmatch(line)
		switch {
		case fence == "" && m != nil:
			fence = m[1]
			lines[i] = ""
		case fence != "":
			if m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) &&
				strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), m[1][:1])) == "" {
				fence = ""
			}
			lines[i] = ""
		}
	}
	return lines
}

// stripCodeSpans removes inline code spans: a run of backticks up to the next run of the same length.
func stripCodeSpans(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		closeAt := -1
		for j := i + n; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(line) && line[j+m] == '`' {
				m++
			}
			if m == n {
				closeAt = j + m
				break
			}
			j += m
		}
		if closeAt < 0 {
			b.WriteString(line[i : i+n])
			i += n
			continue
		}
		i = closeAt
	}
	return b.String()
}

var (
	headingRE = regexp.MustCompile(`^ {0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	linkRE    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	refDefRE  = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(\S+)`)
)

// headingAnchor turns a heading's text into its anchor by GitHub's rule, before repeats are numbered.
func headingAnchor(heading string) string {
	text := linkRE.ReplaceAllString(heading, "$1")
	text = strings.ReplaceAll(text, "`", "")
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

type heading struct {
	level int
	text  string
}

func headings(text string) []heading {
	var hs []heading
	for _, line := range proseLines(text) {
		if m := headingRE.FindStringSubmatch(line); m != nil {
			hs = append(hs, heading{level: len(m[1]), text: m[2]})
		}
	}
	return hs
}

// anchors returns the set of anchors a page's headings give, with -1, -2, … added to repeats.
func anchors(text string) map[string]bool {
	set := map[string]bool{}
	seen := map[string]int{}
	for _, h := range headings(text) {
		a := headingAnchor(h.text)
		if n := seen[a]; n > 0 {
			set[a+"-"+strconv.Itoa(n)] = true
		} else {
			set[a] = true
		}
		seen[a]++
	}
	return set
}

// linkTargets returns each link target on a prose line (code spans already removed).
func linkTargets(line string) []string {
	var targets []string
	if m := refDefRE.FindStringSubmatch(line); m != nil {
		targets = append(targets, strings.Trim(m[1], "<>"))
	}
	for i := 0; ; {
		k := strings.Index(line[i:], "](")
		if k < 0 {
			break
		}
		start := i + k + 2
		rest := line[start:]
		var target string
		if strings.HasPrefix(rest, "<") {
			if end := strings.IndexByte(rest, '>'); end > 0 {
				target = rest[1:end]
			}
		} else {
			depth, end := 0, len(rest)
		scan:
			for j, r := range rest {
				switch {
				case r == '(':
					depth++
				case r == ')' && depth == 0, r == ' ', r == '\t':
					end = j
					break scan
				case r == ')':
					depth--
				}
			}
			target = rest[:end]
		}
		targets = append(targets, target)
		i = start
	}
	return targets
}

var schemeRE = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// TestLinks checks that every relative link and #anchor in every Markdown file of the repo resolves.
func TestLinks(t *testing.T) {
	root := repoRoot(t)
	anchorCache := map[string]map[string]bool{}
	pageAnchors := func(path string) (map[string]bool, error) {
		if a, ok := anchorCache[path]; ok {
			return a, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		a := anchors(string(data))
		anchorCache[path] = a
		return a, nil
	}

	files := markdownFiles(t, root)
	if len(files) == 0 {
		t.Fatal("found no Markdown files")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, file)
		for n, line := range proseLines(string(data)) {
			for _, target := range linkTargets(stripCodeSpans(line)) {
				if target == "" || schemeRE.MatchString(target) {
					continue
				}
				where := rel + ":" + strconv.Itoa(n+1) + ": " + target
				pathPart, anchor, hasAnchor := strings.Cut(target, "#")
				pathPart, err := url.PathUnescape(pathPart)
				if err != nil {
					t.Errorf("%s: bad escape: %v", where, err)
					continue
				}
				dest := file
				switch {
				case pathPart == "":
				case strings.HasPrefix(pathPart, "/"):
					dest = filepath.Join(root, filepath.FromSlash(pathPart))
				default:
					dest = filepath.Join(filepath.Dir(file), filepath.FromSlash(pathPart))
				}
				info, err := os.Stat(dest)
				if err != nil {
					t.Errorf("%s: no such file", where)
					continue
				}
				if !hasAnchor || info.IsDir() || !strings.HasSuffix(dest, ".md") {
					continue
				}
				a, err := pageAnchors(dest)
				if err != nil {
					t.Fatal(err)
				}
				anchor, err = url.PathUnescape(anchor)
				if err != nil || !a[anchor] {
					t.Errorf("%s: no heading has anchor #%s", where, anchor)
				}
			}
		}
	}
}

func TestHeadingAnchor(t *testing.T) {
	cases := []struct{ heading, want string }{
		{"9. Acceptance list", "9-acceptance-list"},
		{"`internal/cli` and `cmd/abyssctl`: start-up, the two checks, `version`", "internalcli-and-cmdabyssctl-start-up-the-two-checks-version"},
		{"`docs/guides/certificates.md`", "docsguidescertificatesmd"},
		{"See [cli.md](cli.md) now", "see-climd-now"},
		{"What's left to you", "whats-left-to-you"},
		{"a  b", "a--b"},
		{"snake_case & more", "snake_case--more"},
	}
	for _, c := range cases {
		if got := headingAnchor(c.heading); got != c.want {
			t.Errorf("headingAnchor(%q) = %q, want %q", c.heading, got, c.want)
		}
	}
	page := "# A\n## Same\n```\n## Not a heading\n```\n## Same\n## Same\n"
	got := anchors(page)
	for _, want := range []string{"a", "same", "same-1", "same-2"} {
		if !got[want] {
			t.Errorf("anchors(page) lacks %q; has %v", want, got)
		}
	}
	if got["not-a-heading"] {
		t.Error("a heading inside a code fence gave an anchor")
	}
}

func TestLinkTargets(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"See [a](a.md) and [b](../b.md#x).", []string{"a.md", "../b.md#x"}},
		{"Code `[a](nope.md)` is skipped, [c](c.md) is not.", []string{"c.md"}},
		{`[t](t.md "title")`, []string{"t.md"}},
		{"[w](https://example.com/x_(y))", []string{"https://example.com/x_(y)"}},
		{"[ref]: ref.md", []string{"ref.md"}},
	}
	for _, c := range cases {
		if got := linkTargets(stripCodeSpans(c.line)); !slices.Equal(got, c.want) {
			t.Errorf("linkTargets(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

// h2s returns a Markdown file's H2 headings in order.
func h2s(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s does not exist", path)
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, h := range headings(string(data)) {
		if h.level == 2 {
			out = append(out, h.text)
		}
	}
	return out
}

func checkSections(t *testing.T, rel string, want []string) {
	t.Helper()
	got := h2s(t, filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if got != nil && !slices.Equal(got, want) {
		t.Errorf("%s H2 sections:\n got %q\nwant %q", rel, got, want)
	}
}

// TestGuides_Sections checks each guide's H2 sections, in order, against docs.md's docs index.
func TestGuides_Sections(t *testing.T) {
	guides := []struct {
		file     string
		sections []string
	}{
		{"docs/guides/certificates.md", []string{
			"What Abyss needs", "Installing a Certificate", "Which Certificate serves a Hostname", "Renewing",
			"Listing and removing", "Expiry warnings",
		}},
		{"docs/guides/backup-and-restore.md", []string{
			"Backups and their kinds", "The nightly Backup", "Backups by hand", "Checking a Backup", "Restoring",
			"What devices see", "An interrupted Restore", "Removed Instances", "Restoring by hand",
		}},
		{"docs/guides/upgrades.md", []string{
			"Upgrading an Instance", "Which versions Abyss picks", "When an Upgrade fails", "When Abyss needs you",
			"Undoing an Upgrade later", "Upgrading the Proxy", "Freeing disk space", "Updating Abyss itself",
		}},
		{"docs/guides/alerts.md", []string{
			"What the Health check looks at", "Problems", "Clearing a Problem", "Setting up Pushover",
			"What an Alert looks like", "The MOTD", "Checking by hand",
		}},
		{"docs/guides/security.md", []string{
			"What Abyss keeps secret", "Files and owners", "Containers", "Internet access", "Backups and logs",
			"Pushover keys", "What leaves the Host", "Docker", "What's left to you", "Reporting a vulnerability",
		}},
		{"docs/guides/teardown.md", []string{
			"Before you start", "Stop the timers", "Remove each Instance", "Remove the Proxy and networks",
			"Remove the Host files", "Remove the account", "Delete the Data root", "Docker",
		}},
	}
	for _, g := range guides {
		t.Run(filepath.Base(g.file), func(t *testing.T) { checkSections(t, g.file, g.sections) })
	}
}

// TestContributing_Sections checks CONTRIBUTING.md's H2 sections, in order, against docs.md's docs index.
func TestContributing_Sections(t *testing.T) {
	checkSections(t, "CONTRIBUTING.md", []string{
		"Before you start", "How Abyss is built", "Build and test", "Docker fixtures", "Running the journeys",
		"Docs", "Pull requests", "Releases", "License",
	})
}
