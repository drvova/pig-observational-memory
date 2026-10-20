// Package jsfmt reproduces the JavaScript string behaviors the extension's
// messages depend on: UTF-16 lengths and en-US number grouping.
package jsfmt

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// UTF16Len is a JavaScript string's length: its UTF-16 code units.
func UTF16Len(text string) int {
	units := 0
	for _, r := range text {
		units++
		if r >= 0x10000 {
			units++
		}
	}
	return units
}

// Units is the UTF-16 code units of text.
func Units(text string) []uint16 { return utf16.Encode([]rune(text)) }

// FromUnits is the string of UTF-16 code units; an unpaired surrogate becomes U+FFFD.
func FromUnits(units []uint16) string { return string(utf16.Decode(units)) }

// Slice is JavaScript String.prototype.slice over UTF-16 units with a start
// and end; negative indices count from the end.
func Slice(text string, start, end int) string {
	units := Units(text)
	n := len(units)
	norm := func(i int) int {
		if i < 0 {
			i += n
			if i < 0 {
				i = 0
			}
		}
		if i > n {
			i = n
		}
		return i
	}
	s, e := norm(start), norm(end)
	if s >= e {
		return ""
	}
	return FromUnits(units[s:e])
}

// Head is the first n UTF-16 units of text.
func Head(text string, n int) string { return Slice(text, 0, n) }

// Tail is the last n UTF-16 units of text.
func Tail(text string, n int) string {
	if n <= 0 {
		return ""
	}
	return Slice(text, -n, UTF16Len(text))
}

// Locale is Number.prototype.toLocaleString() for an integer in en-US: digits
// grouped by three with commas.
func Locale(n int) string {
	negative := n < 0
	if negative {
		n = -n
	}
	digits := strconv.Itoa(n)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

// Plural appends an "s" unless n is 1.
func Plural(n int, singular string) string {
	if n == 1 {
		return singular
	}
	return singular + "s"
}
