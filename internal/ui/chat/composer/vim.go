package composer

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/keybind"
	"github.com/ayn2op/tview/textarea"
	"github.com/gdamore/tcell/v3"
	"github.com/rockorager/go-uucode"
)

// vimState is the state of vim mode: which mode the composer is in and what normal mode remembers between keys.
type vimState struct {
	normal bool
	// pending is the keys of the unfinished normal mode command.
	pending []string
	// find is the last f, F, t or T and the character after it, for ; and , to repeat.
	find [2]string
	// register is the text yanked or deleted last, which p and P put, whole lines if linewise.
	register string
	linewise bool
	// want is the column j and k keep to, or math.MaxInt for the end of the line.
	want int
}

// buffer is a text split into grapheme clusters, which normal mode moves over and edits by index.
type buffer struct {
	text string
	// offs is the byte offset of each cluster, then the length of text.
	offs []int
}

func newBuffer(text string) buffer {
	offs := make([]int, 0, len(text)+1)
	it := uucode.NewGraphemeIterator(text)
	for g, ok := it.Next(); ok; g, ok = it.Next() {
		offs = append(offs, g.Start)
	}
	return buffer{text: text, offs: append(offs, len(text))}
}

func (b buffer) len() int { return len(b.offs) - 1 }

// at returns the cluster at i, or "" past the end.
func (b buffer) at(i int) string {
	if i < 0 || i >= b.len() {
		return ""
	}
	return b.text[b.offs[i]:b.offs[i+1]]
}

// index returns the index of the cluster that starts at or holds the byte offset off.
func (b buffer) index(off int) int {
	i := sort.SearchInts(b.offs, off)
	if i < len(b.offs) && b.offs[i] == off {
		return i
	}
	return i - 1
}

// slice returns the text of the clusters from start to end.
func (b buffer) slice(start, end int) string { return b.text[b.offs[start]:b.offs[end]] }

// replace returns the text with the clusters from start to end replaced by text.
func (b buffer) replace(start, end int, text string) string {
	return b.text[:b.offs[start]] + text + b.text[b.offs[end]:]
}

func (b buffer) isNewline(i int) bool { return b.at(i) == "\n" }

func (b buffer) isBlank(i int) bool {
	r, _ := utf8.DecodeRuneInString(b.at(i))
	return r != '\n' && unicode.IsSpace(r)
}

// lineStart returns the index of the first cluster of the line holding i.
func (b buffer) lineStart(i int) int {
	for i > 0 && !b.isNewline(i-1) {
		i--
	}
	return i
}

// lineEnd returns the index of the newline ending the line holding i, or the length if it is the last line.
func (b buffer) lineEnd(i int) int {
	for i < b.len() && !b.isNewline(i) {
		i++
	}
	return i
}

func (b buffer) firstNonBlank(i int) int {
	i, end := b.lineStart(i), b.lineEnd(i)
	for i < end && b.isBlank(i) {
		i++
	}
	return i
}

// line returns the number of the line holding i, from 0.
func (b buffer) line(i int) int {
	return strings.Count(b.text[:b.offs[i]], "\n")
}

func (b buffer) lines() int { return strings.Count(b.text, "\n") + 1 }

// lineAt returns the index of the first cluster of line n.
func (b buffer) lineAt(n int) int {
	i := 0
	for ; n > 0; n-- {
		i = b.lineEnd(i) + 1
	}
	return i
}

// clamp returns i moved onto a cluster of its line, as the cursor in normal mode never rests on the newline ending a line that has text.
func (b buffer) clamp(i int) int {
	i = min(max(i, 0), b.len())
	start, end := b.lineStart(i), b.lineEnd(i)
	return max(min(i, end-1), start)
}

// class returns 0 for whitespace, 1 for punctuation and 2 for letters, digits and underscores, as vim tells words apart; big makes everything but whitespace 1, for WORDs.
func (b buffer) class(i int, big bool) int {
	r, _ := utf8.DecodeRuneInString(b.at(i))
	switch {
	case unicode.IsSpace(r):
		return 0
	case big:
		return 1
	case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
		return 2
	}
	return 1
}

// wordForward returns the start of the next word after i, stopping at empty lines.
func (b buffer) wordForward(i int, big bool) int {
	n := b.len()
	if class := b.class(i, big); class != 0 {
		for i < n && b.class(i, big) == class {
			i++
		}
	}
	for i < n && b.class(i, big) == 0 {
		if b.isNewline(i) && b.isNewline(i+1) {
			return i + 1
		}
		i++
	}
	return i
}

// wordEnd returns the end of the word after i.
func (b buffer) wordEnd(i int, big bool) int {
	n := b.len()
	i++
	for i < n && b.class(i, big) == 0 {
		i++
	}
	if i >= n {
		return max(n-1, 0)
	}
	for i+1 < n && b.class(i+1, big) == b.class(i, big) {
		i++
	}
	return i
}

// wordBackward returns the start of the word before i, stopping at empty lines.
func (b buffer) wordBackward(i int, big bool) int {
	if i <= 0 {
		return 0
	}
	i--
	for i > 0 && b.class(i, big) == 0 {
		if b.isNewline(i) && b.isNewline(i-1) {
			return i
		}
		i--
	}
	for class := b.class(i, big); i > 0 && b.class(i-1, big) == class; {
		i--
	}
	return i
}

type motionKind int

const (
	exclusive motionKind = iota
	inclusive
	linewise
)

type parse int

const (
	parsed parse = iota
	incomplete
	invalid
)

// motion is where a motion goes and how much of the text an operator on it takes.
type motion struct {
	to   int
	kind motionKind
	// want is the column j and k keep to after the motion, or -1 to keep the one before.
	want int
}

// count reads the count at the start of keys, returning it, whether there was one, and the keys after it.
func count(keys []string) (int, bool, []string) {
	n, i := 0, 0
	for ; i < len(keys); i++ {
		digit, err := strconv.Atoi(keys[i])
		if err != nil || len(keys[i]) != 1 || (digit == 0 && i == 0) {
			break
		}
		n = min(n*10+digit, 9999)
	}
	if i == 0 {
		return 1, false, keys
	}
	return n, true, keys[i:]
}

// motion parses the motion of keys from cur, repeated n times, for an operator if op is not "".
func (m *Model) motion(b buffer, cur int, keys []string, n int, counted bool, op string) (motion, parse) {
	if len(keys) == 0 {
		return motion{}, incomplete
	}
	start, end := b.lineStart(cur), b.lineEnd(cur)
	to := cur
	switch key := keys[0]; key {
	case "h":
		return motion{to: max(cur-n, start), want: max(cur-n, start) - start}, parsed
	case "l":
		limit := end
		if op == "" {
			limit = max(end-1, start)
		}
		return motion{to: min(cur+n, limit), want: min(cur+n, limit) - start}, parsed
	case "j", "k":
		line := b.line(cur)
		target := line + n
		if key == "k" {
			target = line - n
		}
		if target < 0 || target >= b.lines() {
			if (key == "k" && line == 0) || (key == "j" && line == b.lines()-1) {
				return motion{}, invalid
			}
			target = min(max(target, 0), b.lines()-1)
		}
		if m.vim.want < 0 {
			m.vim.want = cur - start
		}
		lineStart := b.lineAt(target)
		to := lineStart + min(m.vim.want, max(b.lineEnd(lineStart)-lineStart-1, 0))
		return motion{to: to, kind: linewise, want: -1}, parsed
	case "w", "W", "b", "B", "e", "E":
		big := key == "W" || key == "B" || key == "E"
		// cw changes to the end of the word, not up to the next one.
		if op == "c" && (key == "w" || key == "W") && b.class(cur, big) != 0 {
			key = "e"
		}
		kind := exclusive
		for range n {
			switch key {
			case "w", "W":
				to = b.wordForward(to, big)
			case "b", "B":
				to = b.wordBackward(to, big)
			default:
				to, kind = b.wordEnd(to, big), inclusive
			}
		}
		// An operator on words that reach the next line stops at the end of the line the last of them is on.
		if op != "" && (key == "w" || key == "W") && to > end {
			to = max(b.lineStart(to)-1, cur)
		}
		return motion{to: to, kind: kind, want: to - b.lineStart(to)}, parsed
	case "0":
		return motion{to: start, want: 0}, parsed
	case "^":
		to = b.firstNonBlank(cur)
		return motion{to: to, want: to - start}, parsed
	case "$":
		to = b.lineEnd(b.lineAt(min(b.line(cur)+n-1, b.lines()-1)))
		return motion{to: to, want: math.MaxInt}, parsed
	case "G", "g":
		line := b.lines() - 1
		if key == "g" {
			if len(keys) < 2 {
				return motion{}, incomplete
			}
			if keys[1] != "g" {
				return motion{}, invalid
			}
			line = 0
		}
		if counted {
			line = min(n-1, b.lines()-1)
		}
		to = b.firstNonBlank(b.lineAt(line))
		return motion{to: to, kind: linewise, want: to - b.lineAt(line)}, parsed
	case "f", "F", "t", "T":
		if len(keys) < 2 {
			return motion{}, incomplete
		}
		m.vim.find = [2]string{key, keys[1]}
		return b.find(cur, key, keys[1], n)
	case ";", ",":
		key, char := m.vim.find[0], m.vim.find[1]
		if key == "" {
			return motion{}, invalid
		}
		if keys[0] == "," {
			key = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[key]
		}
		return b.find(cur, key, char, n)
	}
	return motion{}, invalid
}

// find returns the motion to the nth char after cur on its line for f and t, or before it for F and T.
func (b buffer) find(cur int, key, char string, n int) (motion, parse) {
	start, end := b.lineStart(cur), b.lineEnd(cur)
	forward := key == "f" || key == "t"
	i := cur
	if key == "t" && b.at(i+1) == char {
		i++
	}
	if key == "T" && b.at(i-1) == char {
		i--
	}
	for range n {
		for {
			if forward {
				i++
			} else {
				i--
			}
			if i < start || i >= end {
				return motion{}, invalid
			}
			if b.at(i) == char {
				break
			}
		}
	}
	switch key {
	case "t":
		i--
	case "T":
		i++
	}
	kind := exclusive
	if forward {
		kind = inclusive
	}
	return motion{to: i, kind: kind, want: i - start}, parsed
}

// textObject returns the range of the text object of keys, such as "iw" or "a(", around cur.
func (b buffer) textObject(cur int, keys []string) (int, int, parse) {
	if len(keys) < 2 {
		return 0, 0, incomplete
	}
	around := keys[0] == "a"
	start, end := b.lineStart(cur), b.lineEnd(cur)
	switch object := keys[1]; object {
	case "w", "W":
		if cur >= end {
			return 0, 0, invalid
		}
		big := object == "W"
		run := func(i int) (int, int) {
			class := b.class(i, big)
			s, e := i, i+1
			for s > start && b.class(s-1, big) == class {
				s--
			}
			for e < end && b.class(e, big) == class {
				e++
			}
			return s, e
		}
		s, e := run(cur)
		if !around {
			return s, e, parsed
		}
		switch {
		case b.class(cur, big) == 0 && e < end:
			_, e = run(e)
		case e < end && b.isBlank(e):
			_, e = run(e)
		default:
			for s > start && b.isBlank(s-1) {
				s--
			}
		}
		return s, e, parsed
	case `"`, "'", "`":
		var quotes []int
		for i := start; i < end; i++ {
			if b.at(i) == object && b.at(i-1) != `\` {
				quotes = append(quotes, i)
			}
		}
		for i := 0; i+1 < len(quotes); i += 2 {
			open, close := quotes[i], quotes[i+1]
			if cur > close {
				continue
			}
			if around {
				return open, close + 1, parsed
			}
			return open + 1, close, parsed
		}
		return 0, 0, invalid
	}

	pairs := map[string][2]string{
		"(": {"(", ")"}, ")": {"(", ")"}, "b": {"(", ")"},
		"[": {"[", "]"}, "]": {"[", "]"},
		"{": {"{", "}"}, "}": {"{", "}"}, "B": {"{", "}"},
		"<": {"<", ">"}, ">": {"<", ">"},
	}
	pair, ok := pairs[keys[1]]
	if !ok {
		return 0, 0, invalid
	}
	open, close := -1, -1
	for i, depth := cur, 0; i >= 0; i-- {
		switch b.at(i) {
		case pair[1]:
			if i != cur {
				depth++
			}
		case pair[0]:
			if depth == 0 {
				open = i
			}
			depth--
		}
		if open >= 0 {
			break
		}
	}
	if open < 0 {
		return 0, 0, invalid
	}
	for i, depth := open+1, 0; i < b.len() && close < 0; i++ {
		switch b.at(i) {
		case pair[0]:
			depth++
		case pair[1]:
			if depth == 0 {
				close = i
			}
			depth--
		}
	}
	if close < 0 {
		return 0, 0, invalid
	}
	if around {
		return open, close + 1, parsed
	}
	return open + 1, close, parsed
}

// vimKey returns the key of msg as normal mode reads it, and whether it is a character typed rather than a special key.
func vimKey(msg tview.KeyMsg) (string, bool) {
	if msg.Key() == tcell.KeyRune && msg.Modifiers()&^tcell.ModShift == 0 {
		return msg.Str(), true
	}
	switch keybind.String(msg) {
	case "left", "backspace":
		return "h", false
	case "right":
		return "l", false
	case "up":
		return "k", false
	case "down":
		return "j", false
	case "home":
		return "0", false
	case "end":
		return "$", false
	case "delete":
		return "x", false
	case "ctrl+r":
		return "ctrl+r", false
	}
	return "", false
}

// normalKey adds msg to the pending normal mode command and runs it once it is complete.
func (m *Model) normalKey(msg tview.KeyMsg) {
	key, typed := vimKey(msg)
	if key == "" {
		m.vim.pending = nil
		return
	}
	// The character after f, t or r is taken as it is; special keys there cancel.
	if last := len(m.vim.pending) - 1; last >= 0 && strings.Contains("fFtTr", m.vim.pending[last]) && !typed {
		m.vim.pending = nil
		return
	}
	m.vim.pending = append(m.vim.pending, key)
	if m.runNormal(m.vim.pending) != incomplete {
		m.vim.pending = nil
	}
}

// runNormal runs the normal mode command of keys if it is complete.
func (m *Model) runNormal(keys []string) parse {
	b := newBuffer(m.text)
	cur := b.index(m.cursor)
	n, counted, keys := count(keys)
	if len(keys) == 0 {
		return incomplete
	}

	switch key := keys[0]; key {
	case "d", "c", "y":
		n2, counted2, rest := count(keys[1:])
		n, counted = n*n2, counted || counted2
		if len(rest) == 0 {
			return incomplete
		}
		switch {
		case rest[0] == key:
			last := b.lineAt(min(b.line(cur)+n-1, b.lines()-1))
			m.operate(b, key, cur, motion{to: last, kind: linewise})
		case rest[0] == "i" || rest[0] == "a":
			start, end, p := b.textObject(cur, rest)
			if p != parsed {
				return p
			}
			m.operateRange(b, key, start, end, start)
		default:
			mo, p := m.motion(b, cur, rest, n, counted, key)
			if p != parsed {
				return p
			}
			m.operate(b, key, cur, mo)
		}
	case "x", "s":
		if end := b.lineEnd(cur); cur < end {
			m.operateRange(b, "d", cur, min(cur+n, end), cur)
		}
		if key == "s" {
			m.vim.normal = false
		}
	case "X":
		if start := b.lineStart(cur); cur > start {
			m.operateRange(b, "d", max(cur-n, start), cur, max(cur-n, start))
		}
	case "D", "C", "Y", "S":
		op := map[string]string{"D": "d", "C": "c", "Y": "y", "S": "c"}[key]
		last := b.lineAt(min(b.line(cur)+n-1, b.lines()-1))
		if key == "D" || key == "C" {
			m.operate(b, op, cur, motion{to: b.lineEnd(last)})
		} else {
			m.operate(b, op, cur, motion{to: last, kind: linewise})
		}
	case "r":
		if len(keys) < 2 {
			return incomplete
		}
		if cur+n > b.lineEnd(cur) {
			return invalid
		}
		m.setText(b.replace(cur, cur+n, strings.Repeat(keys[1], n)), 0)
		m.cursor = newBuffer(m.text).offs[cur+n-1]
	case "~":
		end := min(cur+n, b.lineEnd(cur))
		var toggled strings.Builder
		for _, r := range b.slice(cur, end) {
			if unicode.IsUpper(r) {
				r = unicode.ToLower(r)
			} else {
				r = unicode.ToUpper(r)
			}
			toggled.WriteRune(r)
		}
		m.setText(b.replace(cur, end, toggled.String()), 0)
		after := newBuffer(m.text)
		m.cursor = after.offs[min(end, after.len())]
	case "J":
		m.join(b, cur, max(n, 2)-1)
	case "p", "P":
		m.put(b, cur, key == "P", n)
	case "i", "a", "I", "A", "o", "O":
		m.startInsert(b, cur, key)
	case "u":
		for range n {
			m.undo()
		}
	case "ctrl+r":
		for range n {
			m.redoEdit()
		}
	default:
		mo, p := m.motion(b, cur, keys, n, counted, "")
		if p != parsed {
			return p
		}
		m.cursor = b.offs[b.clamp(mo.to)]
		if mo.want >= 0 {
			m.vim.want = mo.want
		}
		return parsed
	}
	m.vim.want = -1
	m.clampCursor()
	return parsed
}

// clampCursor moves the cursor onto a cluster of its line while in normal mode.
func (m *Model) clampCursor() {
	if !m.vim.normal {
		return
	}
	b := newBuffer(m.text)
	m.cursor = b.offs[b.clamp(b.index(m.cursor))]
}

// operate applies op to the text cur and the motion mo span.
func (m *Model) operate(b buffer, op string, cur int, mo motion) {
	start, end := min(cur, mo.to), max(cur, mo.to)
	if mo.kind == inclusive {
		end = min(end+1, b.len())
	}
	if mo.kind != linewise {
		m.operateRange(b, op, start, end, start)
		return
	}

	first, last := b.lineStart(start), b.lineEnd(end)
	m.vim.register, m.vim.linewise = b.slice(first, last)+"\n", true
	switch op {
	case "y":
		// Yanking lines above moves the cursor up to them.
		if b.line(mo.to) < b.line(cur) {
			m.cursor = b.offs[mo.to]
		}
	case "c":
		m.setText(b.replace(first, last, ""), b.offs[first])
		m.vim.normal = false
	case "d":
		// The newline after the lines goes with them, or the one before if they are the last.
		start, end := first, last
		if end < b.len() {
			end++
		} else if start > 0 {
			start--
		}
		m.setText(b.replace(start, end, ""), b.offs[start])
		after := newBuffer(m.text)
		m.cursor = after.offs[after.firstNonBlank(after.index(m.cursor))]
	}
}

// operateRange applies op to the clusters from start to end, leaving the cursor at cursor if it yanks.
func (m *Model) operateRange(b buffer, op string, start, end, cursor int) {
	m.vim.register, m.vim.linewise = b.slice(start, end), false
	switch op {
	case "y":
		m.cursor = b.offs[cursor]
	case "c":
		m.setText(b.replace(start, end, ""), b.offs[start])
		m.vim.normal = false
	case "d":
		m.setText(b.replace(start, end, ""), b.offs[start])
	}
}

// join joins the line holding cur with the times lines after it, putting a space between them.
func (m *Model) join(b buffer, cur, times int) {
	text, at := b.text, -1
	for range times {
		b = newBuffer(text)
		end := b.lineEnd(cur)
		if end >= b.len() {
			break
		}
		next := end + 1
		for next < b.len() && b.isBlank(next) {
			next++
		}
		sep := " "
		if end == b.lineStart(end) || b.isBlank(end-1) || b.isNewline(next) || next == b.len() || b.at(next) == ")" {
			sep = ""
		}
		at = b.offs[end]
		text = b.replace(end, next, sep)
	}
	if at < 0 {
		return
	}
	m.setText(text, at)
}

// put puts the register after cur, or at it if before, n times.
func (m *Model) put(b buffer, cur int, before bool, n int) {
	if m.vim.register == "" {
		return
	}
	text := strings.Repeat(m.vim.register, n)
	if m.vim.linewise {
		at := b.lineStart(cur)
		if !before {
			at = b.lineEnd(cur)
			if at == b.len() {
				// The last line has no newline to put the lines after.
				m.setText(b.text+"\n"+strings.TrimSuffix(text, "\n"), 0)
				after := newBuffer(m.text)
				m.cursor = after.offs[after.firstNonBlank(b.len()+1)]
				return
			}
			at++
		}
		m.setText(b.replace(at, at, text), 0)
		after := newBuffer(m.text)
		m.cursor = after.offs[after.firstNonBlank(at)]
		return
	}

	at := cur
	if !before && cur < b.lineEnd(cur) {
		at++
	}
	m.setText(b.replace(at, at, text), 0)
	// The cursor goes on the last cluster put.
	m.cursor = b.offs[at] + len(text) - 1
	after := newBuffer(m.text)
	m.cursor = after.offs[after.index(m.cursor)]
}

// startInsert enters insert mode for key: before the cursor, after it, at the line's start or end, or on a new line below or above.
func (m *Model) startInsert(b buffer, cur int, key string) {
	m.vim.normal = false
	switch key {
	case "a":
		if cur < b.lineEnd(cur) {
			cur++
		}
		m.cursor = b.offs[cur]
	case "I":
		m.cursor = b.offs[b.firstNonBlank(cur)]
	case "A":
		m.cursor = b.offs[b.lineEnd(cur)]
	case "o":
		end := b.offs[b.lineEnd(cur)]
		m.setText(b.text[:end]+"\n"+b.text[end:], end+1)
	case "O":
		start := b.offs[b.lineStart(cur)]
		m.setText(b.text[:start]+"\n"+b.text[start:], start)
	}
}

// enterNormal leaves insert mode, moving the cursor onto the character before it as vim does.
func (m *Model) enterNormal() {
	m.vim.normal, m.vim.pending, m.vim.want = true, nil, -1
	b := newBuffer(m.text)
	cur := b.index(m.cursor)
	if cur > b.lineStart(cur) {
		cur--
	}
	m.cursor = b.offs[b.clamp(cur)]
}

// vimCancel leaves insert mode, or else drops the unfinished command, or else hides the completions, or else cancels the reply, edit, post and attachments.
func (m *Model) vimCancel() tview.Cmd {
	switch {
	case !m.vim.normal:
		m.enterNormal()
		if m.autocomplete.Visible() {
			return m.showCompletions("", nil)
		}
	case len(m.vim.pending) > 0:
		m.vim.pending = nil
	case m.autocomplete.Visible():
		return m.showCompletions("", nil)
	default:
		m.cancel()
	}
	return nil
}

// normalArea is the text area in normal mode, which draws the text and cursor but leaves keys to Update.
type normalArea struct {
	textarea.Widget
}

func (a normalArea) Handle(msg tview.Msg, area tview.Rectangle) tview.Msg {
	if _, ok := msg.(tview.KeyMsg); ok {
		return msg
	}
	return a.Widget.Handle(msg, area)
}

// cursorStyle draws child after setting the shape of the terminal's cursor: a block in normal mode, or the terminal's own otherwise.
type cursorStyle struct {
	tview.Widget
	style tcell.CursorStyle
}

func (c cursorStyle) Draw(screen tview.Screen, area tview.Rectangle) {
	screen.SetCursorStyle(c.style)
	c.Widget.Draw(screen, area)
}
