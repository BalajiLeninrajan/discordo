package composer

import (
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/tview"
	"github.com/gdamore/tcell/v3"
)

// newVimModel returns a focused composer in vim mode showing text, where "|" marks the cursor, in normal mode.
func newVimModel(t *testing.T, text string) *Model {
	t.Helper()
	c, cfg := newTestModel(t)
	cfg.Composer.VimMode = true
	c.SetFocused(true)
	cursor := strings.Index(text, "|")
	c.text, c.cursor = strings.Replace(text, "|", "", 1), cursor
	c.vim.normal, c.vim.want = true, -1
	return c
}

// press types keys into c as the app would, with <esc> for escape and <c-r> for ctrl+r.
func press(c *Model, keys string) {
	area := tview.Rectangle{Width: 40, Height: 5}
	for keys != "" {
		var key tview.KeyMsg
		switch {
		case strings.HasPrefix(keys, "<esc>"):
			key, keys = tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone), keys[len("<esc>"):]
		case strings.HasPrefix(keys, "<c-r>"):
			key, keys = tcell.NewEventKey(tcell.KeyCtrlR, "", tcell.ModNone), keys[len("<c-r>"):]
		default:
			key, keys = tcell.NewEventKey(tcell.KeyRune, keys[:1], tcell.ModNone), keys[1:]
		}
		*c, _ = c.Update(c.View().Handle(key, area))
	}
}

func shown(c *Model) string {
	return c.text[:c.cursor] + "|" + c.text[c.cursor:]
}

func TestVimNormalMode(t *testing.T) {
	for _, tt := range []struct {
		name, text, keys, want string
		insert                 bool
	}{
		{"escape leaves insert mode onto the character before", "abc|", "i<esc>", "ab|c", false},
		{"letters do not type", "|abc", "z", "|abc", false},
		{"l stops at the last character", "|abc", "3l", "ab|c", false},
		{"w and b", "foo.b|ar", "bb", "foo|.bar", false},
		{"j and k keep the column", "abc|d\nx\nabcd", "jj", "abcd\nx\nabc|d", false},
		{"dw", "foo |bar baz", "dw", "foo |baz", false},
		{"dw at the end of a line keeps the newline", "foo |bar\nbaz", "dw", "foo| \nbaz", false},
		{"counts multiply", "|a b c", "d2w", "|c", false},
		{"cw changes to the end of the word", "foo b|ar baz", "cwqux<esc>", "foo bqu|x baz", false},
		{"dd", "one\ntw|o\nthree", "dd", "one\n|three", false},
		{"dd on the last line", "one\ntwo\nthr|ee", "dd", "one\n|two", false},
		{"ci(", "a(b|c)d", "ci(x<esc>", "a(|x)d", false},
		{`di"`, `say "hi| there" now`, `di"`, `say "|" now`, false},
		{"daw", "one |two three", "daw", "one |three", false},
		{"yiw and P", "|abc def", "yiwP", "ab|cabc def", false},
		{"yy and p", "|one\ntwo", "yyp", "one\n|one\ntwo", false},
		{"x with a count", "|one two", "2x", "|e two", false},
		{"undo and redo", "a|bc", "xu", "a|bc", false},
		{"redo", "a|bc", "xu<c-r>", "a|c", false},
		{"f and D", "|foo bar", "fbD", "foo| ", false},
		{"; repeats f", "|a,b,c", "f,;x", "a,b|c", false},
		{"J", "|one\n  two", "J", "one| two", false},
		{"j with a count", "|ab\ncd\nef", "2jx", "ab\ncd\n|f", false},
		{"gg and G", "ab\n|cd\nef", "Gx", "ab\ncd\n|f", false},
		{"~", "|abc", "~~", "AB|c", false},
		{"r", "|aaa", "rb", "|baa", false},
		{"A", "|hello", "A!", "hello!|", true},
		{"o", "|hello", "ohi<esc>", "hello\nh|i", false},
		{"I goes to the first non-blank", "  ab|c", "Ix", "  x|abc", true},
		{"$ and a append at the end", "|ab", "$ac", "abc|", true},
		{"escape drops an unfinished command", "|abc", "d<esc>x", "|bc", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newVimModel(t, tt.text)
			press(c, tt.keys)
			if got := shown(c); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if insert := !c.vim.normal; insert != tt.insert {
				t.Errorf("insert mode = %v, want %v", insert, tt.insert)
			}
		})
	}
}

func TestVimCancel(t *testing.T) {
	t.Run("escape keeps the text, then drops the reply", func(t *testing.T) {
		c := newVimModel(t, "|")
		c.vim.normal = false
		c.StartReply(discord.Message{ID: 5}, "ann", true)
		press(c, "hi<esc>")
		if c.sendMessageData.Reference == nil || c.text != "hi" {
			t.Fatalf("after one escape, reference = %v, text = %q", c.sendMessageData.Reference, c.text)
		}
		press(c, "<esc>")
		if c.sendMessageData.Reference != nil || c.text != "hi" {
			t.Fatalf("after two escapes, reference = %v, text = %q", c.sendMessageData.Reference, c.text)
		}
	})
	t.Run("escape in normal mode drops an edit and its text", func(t *testing.T) {
		c := newVimModel(t, "|")
		c.StartEdit(discord.Message{ID: 5, Content: "old"})
		press(c, "<esc><esc>")
		if c.editing != nil || c.text != "" {
			t.Fatalf("editing = %v, text = %q", c.editing, c.text)
		}
	})
	t.Run("sending goes back to insert mode", func(t *testing.T) {
		c := newVimModel(t, "|hi")
		c.reset()
		if c.vim.normal {
			t.Fatal("still in normal mode")
		}
	})
}

func TestVimCommandsKeepTheCursorValid(t *testing.T) {
	texts := []string{"|", "|\n", "\n|\n", "a|", "ab\n\n|cd ef\n", "x 👍🏽 |y\n\tz", `a "b" (c [d] {e}) |f`}
	commands := []string{
		"h", "l", "j", "k", "w", "W", "b", "B", "e", "E", "0", "^", "$", "gg", "G", "fa", "Fa", "ta", "Ta", ";", ",",
		"x", "X", "s<esc>", "S<esc>", "D", "C<esc>", "Y", "rq", "~", "J", "p", "P", "u", "<c-r>",
		"dd", "dw", "db", "de", "d$", "d0", "dj", "dk", "dG", "dgg", "cc<esc>", "cw<esc>", "yy", "yw",
		"diw", "daw", "diW", "daW", `di"`, `da"`, "di(", "da(", "di[", "di{", "da<",
		"3x", "2dd", "5j", "3J", "2p", "o<esc>", "O<esc>", "a<esc>", "A<esc>", "I<esc>", "i<esc>",
	}
	for _, text := range texts {
		for _, keys := range commands {
			c := newVimModel(t, text)
			press(c, "yy"+keys+"P"+keys)
			if c.cursor < 0 || c.cursor > len(c.text) {
				t.Fatalf("%q after %q: cursor %d out of %q", text, keys, c.cursor, c.text)
			}
			if b := newBuffer(c.text); c.vim.normal && b.offs[b.index(c.cursor)] != c.cursor {
				t.Fatalf("%q after %q: cursor %d inside a character of %q", text, keys, c.cursor, c.text)
			}
		}
	}
}
