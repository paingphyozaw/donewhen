package service

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/johnreginald/donewhen/internal/store"
)

// The handoff format (BIZ-283). This file is the one place that holds it: the
// checks on save, the empty template and the rules text all come from these
// values, and the web page reads them from the API instead of keeping a copy.

const (
	HandoffMaxLines = 150
	HandoffMaxChars = 12000
	// HandoffStaleDays is how old the latest version may get before the page
	// calls it stale.
	HandoffStaleDays = 3
)

// HandoffSections are the required headings, in order.
var HandoffSections = []string{
	"## Now",
	"## In flight",
	"## Next steps",
	"## Waiting on owner",
	"## Rules and gotchas",
	"## Where things are",
}

// HandoffRules is the format in short text, for agents and the Rules panel.
const HandoffRules = `Use these six headings, in this order, each once (headings inside code fences do not count):
## Now - the date and 2-4 sentences on where the work stands (6 lines).
## In flight - a table: Ticket | State | PR | Next action | Who. One row per open ticket.
## Next steps - numbered, in order (10 items).
## Waiting on owner - questions and decisions, each with its ticket.
## Rules and gotchas - project rules that are easy to forget (15 bullets).
## Where things are - paths, commands, URLs (15 bullets).
Limits: 150 lines and 12,000 characters in all.
Replace, never append: a save is the full current picture, and history keeps the old versions.
No dated "Update 17:45" logs: progress belongs in ticket comments.
Remove tickets that are Done or Canceled from In flight.`

// HandoffTemplate is the empty handoff: the six headings.
func HandoffTemplate() string {
	return strings.Join(HandoffSections, "\n\n") + "\n"
}

// HandoffFormat is what the API and get_handoff hand out so nobody guesses.
type HandoffFormat struct {
	Rules     string   `json:"rules"`
	Template  string   `json:"template"`
	Sections  []string `json:"sections"`
	MaxLines  int      `json:"maxLines"`
	MaxChars  int      `json:"maxChars"`
	StaleDays int      `json:"staleDays"`
}

func HandoffFormatInfo() HandoffFormat {
	return HandoffFormat{
		Rules: HandoffRules, Template: HandoffTemplate(), Sections: HandoffSections,
		MaxLines: HandoffMaxLines, MaxChars: HandoffMaxChars, StaleDays: HandoffStaleDays,
	}
}

// HandoffFormatError is a save refused for its structure. Code is
// handoff_sections_missing or handoff_too_long. It counts as ErrInvalid.
type HandoffFormatError struct {
	Code    string
	Message string
	Missing []string // for handoff_sections_missing
	Lines   int      // for handoff_too_long
	Chars   int
}

func (e *HandoffFormatError) Error() string { return e.Code + ": " + e.Message }
func (e *HandoffFormatError) Unwrap() error { return store.ErrInvalid }

func normHeading(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// fenceMarker returns the fence character (` or ~) when line opens or closes a
// code fence, else 0.
func fenceMarker(line string) byte {
	t := strings.TrimLeft(line, " ")
	if strings.HasPrefix(t, "```") {
		return '`'
	}
	if strings.HasPrefix(t, "~~~") {
		return '~'
	}
	return 0
}

// handoffParse walks the body once, skipping lines inside code fences when it
// looks for "## " headings. It returns the headings in order (normalised) and
// the text under each, up to the next heading.
func handoffParse(body string) ([]string, map[string]string) {
	var heads []string
	bodies := map[string]string{}
	cur := ""
	var buf []string
	flush := func() {
		if cur != "" {
			bodies[cur] += strings.Join(buf, "\n")
		}
		buf = nil
	}
	var fence byte
	for _, line := range strings.Split(body, "\n") {
		if m := fenceMarker(line); m != 0 && (fence == 0 || fence == m) {
			if fence == 0 {
				fence = m
			} else {
				fence = 0
			}
		} else if fence == 0 && strings.HasPrefix(line, "## ") {
			flush()
			cur = normHeading(line)
			heads = append(heads, cur)
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return heads, bodies
}

func handoffSectionBodies(body string) map[string]string {
	_, b := handoffParse(body)
	return b
}

// CheckHandoffFormat applies the structure rules to a new save.
func CheckHandoffFormat(body string) error {
	heads, _ := handoffParse(body)
	count := map[string]int{}
	for _, h := range heads {
		count[h]++
	}
	var missing, dup []string
	for _, h := range HandoffSections {
		switch n := count[normHeading(h)]; {
		case n == 0:
			missing = append(missing, h)
		case n > 1:
			dup = append(dup, h)
		}
	}
	if len(missing) > 0 {
		return &HandoffFormatError{
			Code:    "handoff_sections_missing",
			Message: "missing sections: " + strings.Join(missing, ", ") + ". Use the template from get_handoff.",
			Missing: missing,
		}
	}
	if len(dup) > 0 {
		return &HandoffFormatError{
			Code:    "handoff_sections_duplicate",
			Message: "each section may appear once; repeated: " + strings.Join(dup, ", "),
			Missing: dup,
		}
	}
	var seen []string
	for _, h := range heads {
		if count[h] == 1 && slices.ContainsFunc(HandoffSections, func(r string) bool { return normHeading(r) == h }) {
			seen = append(seen, h)
		}
	}
	for i, h := range HandoffSections {
		if seen[i] != normHeading(h) {
			return &HandoffFormatError{
				Code:    "handoff_sections_order",
				Message: "sections are out of order; the order is: " + strings.Join(HandoffSections, ", "),
				Missing: HandoffSections,
			}
		}
	}
	lines := strings.Count(strings.TrimRight(body, "\n"), "\n") + 1
	chars := utf8.RuneCountInString(body)
	if lines > HandoffMaxLines || chars > HandoffMaxChars {
		return &HandoffFormatError{
			Code: "handoff_too_long",
			Message: fmt.Sprintf("%d lines and %d characters; the limits are %d lines and %d characters. Replace old detail, do not append.",
				lines, chars, HandoffMaxLines, HandoffMaxChars),
			Lines: lines, Chars: chars,
		}
	}
	return nil
}

var handoffTicketKey = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9]*-\d+\b`)

// HandoffInFlightKeys lists the distinct issue keys named in the In flight
// section, upper-cased, in order of first appearance.
func HandoffInFlightKeys(body string) []string {
	sec := handoffSectionBodies(body)[normHeading("## In flight")]
	seen := map[string]bool{}
	var keys []string
	for _, k := range handoffTicketKey.FindAllString(sec, -1) {
		k = strings.ToUpper(k)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}
