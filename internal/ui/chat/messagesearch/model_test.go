package messagesearch

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
)

// newTestModel returns a search of channel 1 in guild 2.
func newTestModel(t *testing.T) Model {
	t.Helper()
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(cfg, ningen.FromState(state.New("")))
	m.Open(&discord.Channel{ID: 1, GuildID: 2})
	return m
}

// update passes msgs to m in turn and returns it with the command the last one returned.
func update(m Model, msgs ...tview.Msg) (Model, tview.Cmd) {
	var command tview.Cmd
	for _, msg := range msgs {
		m, command = m.Update(msg)
	}
	return m, command
}

// searched returns m after searching for query, with the first page of results.
func searched(t *testing.T, query string, results []discord.Message, total uint) Model {
	t.Helper()
	m, command := update(newTestModel(t), queryMsg{query: query, cursor: len(query)}, submitMsg{})
	if command == nil || !m.loading {
		t.Fatal("submitting a new query does not search")
	}
	m, _ = m.Update(resultsMsg{request: m.request, results: results, total: total})
	return m
}

func messages(ids ...discord.MessageID) []discord.Message {
	out := make([]discord.Message, len(ids))
	for i, id := range ids {
		out[i] = discord.Message{ID: id, ChannelID: 1}
	}
	return out
}

func TestModelSubmit(t *testing.T) {
	t.Run("empty query", func(t *testing.T) {
		if _, command := update(newTestModel(t), queryMsg{query: "  "}, submitMsg{}); command != nil {
			t.Fatal("an empty query searches")
		}
	})

	t.Run("shows results", func(t *testing.T) {
		m := searched(t, "hello", messages(5, 4), 2)
		if len(m.results) != 2 || m.loading || m.status() != "1 of 2" {
			t.Fatalf("results = %d, loading = %v, status = %q", len(m.results), m.loading, m.status())
		}
	})

	t.Run("selects the result for the same query", func(t *testing.T) {
		m, command := update(searched(t, "hello", messages(5, 4), 2), cursorMsg(1), submitMsg{})
		task, ok := command.(cmd.Task)
		if !ok {
			t.Fatalf("command = %T, want a task", command)
		}
		if msg := task(); msg != (SelectedMsg{ChannelID: 1, MessageID: 4}) {
			t.Fatalf("msg = %#v", msg)
		}
		if m.loading {
			t.Fatal("selecting a result searched again")
		}
	})

	t.Run("searches again for a changed query", func(t *testing.T) {
		m, command := update(searched(t, "hello", messages(5, 4), 2), queryMsg{query: "bye", cursor: 3}, submitMsg{})
		if command == nil || m.searched != "bye" || len(m.results) != 0 {
			t.Fatalf("searched = %q, results = %d", m.searched, len(m.results))
		}
	})

	t.Run("retries a failed search", func(t *testing.T) {
		m, command := update(newTestModel(t), queryMsg{query: "hello", cursor: 5}, submitMsg{})
		m, _ = m.Update(resultsMsg{request: m.request, err: errors.New("rate limited")})
		if m.status() != "Search failed" {
			t.Fatalf("status = %q", m.status())
		}
		if m, command = m.Update(submitMsg{}); command == nil || !m.loading {
			t.Fatal("submitting after a failure does not search again")
		}
	})
}

func TestModelResults(t *testing.T) {
	t.Run("drops results of a replaced search", func(t *testing.T) {
		m, _ := update(newTestModel(t), queryMsg{query: "a", cursor: 1}, submitMsg{})
		stale := m.request
		m, _ = update(m, queryMsg{query: "b", cursor: 1}, submitMsg{})
		m, _ = m.Update(resultsMsg{request: stale, results: messages(9), total: 1})
		if len(m.results) != 0 || !m.loading {
			t.Fatal("results of a replaced search were shown")
		}
	})

	t.Run("loads more at the last result", func(t *testing.T) {
		m := searched(t, "hello", messages(5, 4), 3)
		m, command := m.Update(cursorMsg(1))
		if command == nil || !m.loading {
			t.Fatal("selecting the last result loaded does not load more")
		}
		m, _ = m.Update(resultsMsg{request: m.request, offset: 2, results: messages(3), total: 3})
		if len(m.results) != 3 || m.status() != "2 of 3" {
			t.Fatalf("results = %d, status = %q", len(m.results), m.status())
		}
		if _, command = m.Update(cursorMsg(2)); command != nil {
			t.Fatal("loaded more past the total")
		}
	})

	t.Run("an empty page ends the results", func(t *testing.T) {
		m := searched(t, "hello", messages(5), 10)
		m, _ = m.Update(cursorMsg(0))
		m, _ = m.Update(resultsMsg{request: m.request, offset: 1, total: 10})
		if m.total != 1 {
			t.Fatalf("total = %d, want 1", m.total)
		}
	})

	t.Run("no results", func(t *testing.T) {
		if m := searched(t, "hello", nil, 0); m.status() != "No results" {
			t.Fatalf("status = %q", m.status())
		}
	})
}

func TestModelOpen(t *testing.T) {
	m := searched(t, "hello", messages(5), 1)
	m.Open(&discord.Channel{ID: 1, GuildID: 2})
	if m.searched != "hello" || len(m.results) != 1 {
		t.Fatal("reopening the same channel lost the search")
	}

	request := m.request
	m.Open(&discord.Channel{ID: 3})
	if m.query != "" || m.searched != "" || len(m.results) != 0 {
		t.Fatal("opening another channel kept the search")
	}
	if m.request != request {
		t.Fatal("opening another channel reset the request number, so late results could be shown")
	}
}

func TestSnippet(t *testing.T) {
	tests := []struct {
		message discord.Message
		want    string
	}{
		{discord.Message{Content: "two\nlines  here"}, "two lines here"},
		{discord.Message{Attachments: make([]discord.Attachment, 1)}, "[1 attachment]"},
		{discord.Message{Attachments: make([]discord.Attachment, 3)}, "[3 attachments]"},
		{discord.Message{Embeds: make([]discord.Embed, 1)}, "[embed]"},
	}
	for _, test := range tests {
		if got := snippet(test.message); got != test.want {
			t.Errorf("snippet() = %q, want %q", got, test.want)
		}
	}
}

func TestModelView(t *testing.T) {
	const width, height = 60, 12
	screen, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: width, Y: height}))
	if err != nil {
		t.Fatal(err)
	}
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)

	m := searched(t, "lunch", []discord.Message{
		{ID: 5, ChannelID: 1, Author: discord.User{Username: "alice"}, Content: "lunch at\nnoon?", Timestamp: discord.NowTimestamp()},
		{ID: 4, ChannelID: 1, Author: discord.User{Username: "bob"}, Attachments: make([]discord.Attachment, 2), Timestamp: discord.NowTimestamp()},
	}, 7)
	m.View().Draw(screen, tview.Rectangle{Width: width, Height: height})

	var drawn strings.Builder
	for y := range height {
		for x := range width {
			str, _, _ := screen.Get(x, y)
			drawn.WriteString(str)
		}
		drawn.WriteByte('\n')
	}
	for _, want := range []string{"> lunch", "alice", "lunch at noon?", "bob", "[2 attachments]", "1 of 7"} {
		if !strings.Contains(drawn.String(), want) {
			t.Errorf("%q is not drawn in:\n%s", want, drawn.String())
		}
	}
}
