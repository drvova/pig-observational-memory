package ledger

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Relative-date resolution: a closed table of English phrases, each resolved
// against an anchor (the record's cited source time) and annotated in place.
// The table is deliberately small; a phrase it does not know is left as is.
// "in N days/weeks" is resolved only with a future cue (see hasFutureCue),
// since it is as often a duration ("finished in two weeks") as an offset.
//
// Conventions: weeks start on Monday. "last <weekday>" is the most recent such
// day before the anchor and "next <weekday>" the soonest after it; "this
// <weekday>" is that day in the anchor's week. A week resolves to its Monday
// ("week of"), a month to YYYY-MM, a year to YYYY, a weekend to its Saturday.

const relNumber = `(\d{1,3}|an?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)`

var relativeDatePattern = regexp.MustCompile(`(?i)\b(?:` +
	`(?P<couple>a\s+couple\s+(?:of\s+)?days\s+ago)` +
	`|(?P<weekend>(?:last|this)\s+weekend)` +
	`|(?P<weekday>(?:last|this|next)\s+(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday))` +
	`|(?P<period>(?:last|next)\s+(?:week|month|year))` +
	`|(?P<ago>` + relNumber + `\s+(?:day|week|month)s?\s+ago)` +
	`|(?P<in>in\s+` + relNumber + `\s+(?:day|week)s?)` +
	`|(?P<twoday>(?:the\s+)?day\s+(?:before\s+yesterday|after\s+tomorrow))` +
	`|(?P<day>today|yesterday|tomorrow)` +
	`)\b`)

// relativeDateGuard matches text right after a phrase that already carries a
// resolved date: an annotation or a parenthesised ISO or month-name date.
var relativeDateGuard = regexp.MustCompile(`(?i)^\s*(?:\[=|\(\s*(?:\d{4}-\d{2}|(?:jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+\d))`)

// relativeFutureCue marks "in N days/weeks" as a future offset rather than a
// duration ("finished in two weeks"): a cue earlier in the same clause, or
// "from now" / "time" right after the phrase.
var (
	relativeFutureCueBefore = regexp.MustCompile(`(?i)(?:\bwill|'ll|\bgoing\s+to|\bplan(?:s|ning)?\s+to|\bintends?\s+to|\bexpects?\s+to|\bscheduled|\bdue)\b`)
	relativeFutureCueAfter  = regexp.MustCompile(`(?i)^(?:\s+from\s+now|'?\s+time)\b`)
	relativeClauseBreak     = regexp.MustCompile(`[.;!?]`)
)

var relWeekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

var relNumberWords = map[string]int{
	"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12,
}

// ResolveRelativeDates writes the date a relative phrase in content refers to
// right after the phrase, as " [=YYYY-MM-DD]", " [=week of YYYY-MM-DD]",
// " [=YYYY-MM]" or " [=YYYY]", resolved against anchor's calendar date. A
// phrase already followed by an annotation or a parenthesised date, or inside
// double quotes, is left alone. A zero anchor returns content unchanged.
func ResolveRelativeDates(content string, anchor time.Time) string {
	if anchor.IsZero() || content == "" {
		return content
	}
	day := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)
	names := relativeDatePattern.SubexpNames()
	var b strings.Builder
	last := 0
	for _, match := range relativeDatePattern.FindAllStringSubmatchIndex(content, -1) {
		end := match[1]
		for _, possessive := range []string{"'s", "’s"} {
			if strings.HasPrefix(content[end:], possessive) {
				end += len(possessive)
				break
			}
		}
		if relativeDateGuard.MatchString(content[end:]) || insideDoubleQuotes(content[:match[0]]) {
			continue
		}
		annotation := ""
		for group := 1; group < len(names); group++ {
			if names[group] == "" || match[2*group] < 0 {
				continue
			}
			if names[group] == "in" && !hasFutureCue(content[:match[0]], content[end:]) {
				break
			}
			annotation = resolveRelativePhrase(names[group], strings.ToLower(content[match[2*group]:match[2*group+1]]), day)
			break
		}
		if annotation == "" {
			continue
		}
		b.WriteString(content[last:end])
		b.WriteString(" [=" + annotation + "]")
		last = end
	}
	if last == 0 {
		return content
	}
	b.WriteString(content[last:])
	return b.String()
}

// hasFutureCue reports whether "in N days/weeks" reads as a future offset:
// the clause before it carries a future cue, or "from now" follows it.
func hasFutureCue(before, after string) bool {
	if breaks := relativeClauseBreak.FindAllStringIndex(before, -1); len(breaks) > 0 {
		before = before[breaks[len(breaks)-1][1]:]
	}
	return relativeFutureCueBefore.MatchString(before) || relativeFutureCueAfter.MatchString(after)
}

// insideDoubleQuotes reports whether the text before a position leaves a
// double quote open: an odd number of straight quotes, or more opening than
// closing curly quotes.
func insideDoubleQuotes(before string) bool {
	return strings.Count(before, `"`)%2 == 1 || strings.Count(before, "“") > strings.Count(before, "”")
}

// resolveRelativePhrase resolves one lowercased phrase of the named group.
func resolveRelativePhrase(group, phrase string, day time.Time) string {
	words := strings.Fields(phrase)
	switch group {
	case "couple":
		return isoDate(day.AddDate(0, 0, -2))
	case "weekend":
		saturday := weekStart(day).AddDate(0, 0, 5)
		if words[0] == "last" {
			saturday = saturday.AddDate(0, 0, -7)
		}
		return isoDate(saturday)
	case "weekday":
		target := relWeekdays[words[1]]
		switch words[0] {
		case "last":
			diff := (int(day.Weekday()) - int(target) + 7) % 7
			if diff == 0 {
				diff = 7
			}
			return isoDate(day.AddDate(0, 0, -diff))
		case "next":
			diff := (int(target) - int(day.Weekday()) + 7) % 7
			if diff == 0 {
				diff = 7
			}
			return isoDate(day.AddDate(0, 0, diff))
		default:
			offset := (int(target) + 6) % 7
			return isoDate(weekStart(day).AddDate(0, 0, offset))
		}
	case "period":
		sign := 1
		if words[0] == "last" {
			sign = -1
		}
		switch words[1] {
		case "week":
			return "week of " + isoDate(weekStart(day).AddDate(0, 0, 7*sign))
		case "month":
			return yearMonth(day, sign)
		default:
			return strconv.Itoa(day.Year() + sign)
		}
	case "ago":
		n, ok := relativeCount(words[0])
		if !ok {
			return ""
		}
		switch strings.TrimSuffix(words[1], "s") {
		case "day":
			return isoDate(day.AddDate(0, 0, -n))
		case "week":
			return isoDate(day.AddDate(0, 0, -7*n))
		default:
			return yearMonth(day, -n)
		}
	case "in":
		n, ok := relativeCount(words[1])
		if !ok {
			return ""
		}
		if strings.TrimSuffix(words[2], "s") == "week" {
			n *= 7
		}
		return isoDate(day.AddDate(0, 0, n))
	case "twoday":
		if words[len(words)-1] == "yesterday" {
			return isoDate(day.AddDate(0, 0, -2))
		}
		return isoDate(day.AddDate(0, 0, 2))
	case "day":
		switch phrase {
		case "yesterday":
			return isoDate(day.AddDate(0, 0, -1))
		case "tomorrow":
			return isoDate(day.AddDate(0, 0, 1))
		default:
			return isoDate(day)
		}
	}
	return ""
}

func relativeCount(word string) (int, bool) {
	if n, ok := relNumberWords[word]; ok {
		return n, true
	}
	n, err := strconv.Atoi(word)
	return n, err == nil && n > 0
}

// weekStart is the Monday of day's week.
func weekStart(day time.Time) time.Time {
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// yearMonth is the YYYY-MM of the month offset months from day's month.
func yearMonth(day time.Time, offset int) string {
	return time.Date(day.Year(), day.Month()+time.Month(offset), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
}

func isoDate(day time.Time) string {
	return day.Format("2006-01-02")
}
