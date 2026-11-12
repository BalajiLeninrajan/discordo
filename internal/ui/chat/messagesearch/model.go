// Package messagesearch searches the messages of a channel through Discord's search and picks one of the results.
package messagesearch

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
	"github.com/ayn2op/tview/list"
	"github.com/ayn2op/tview/scrollbar"
	"github.com/ayn2op/tview/text"
	"github.com/ayn2op/tview/viewport"
	"github.com/gdamore/tcell/v3"
)

type Model struct {
	cfg   *config.Config
	state *ningen.State

	// channel is the channel searched, or nil before one is.
	channel *discord.Channel

	// query is what is typed, with the cursor at the byte offset queryCursor in it.
	query       string
	queryCursor int
	// searched is the query the results are for, or "" before a search.
	searched string
	// results are the matching messages loaded so far, newest first, of total.
	results []discord.Message
	total   uint
	// cursor is the selected result and scroll how far the results are scrolled.
	cursor int
	scroll viewport.ScrollState
	// loading reports whether a search is on its way, and err why the last one failed.
	loading bool
	err     error
	// request numbers the searches, so that the results of one replaced by another are dropped.
	request int
}

func NewModel(cfg *config.Config, state *ningen.State) Model {
	return Model{cfg: cfg, state: state}
}

// Open searches channel. The query and results are kept if it is the channel searched last, to go through them again.
func (m *Model) Open(channel *discord.Channel) {
	if m.channel != nil && channel != nil && m.channel.ID == channel.ID {
		return
	}
	*m = Model{cfg: m.cfg, state: m.state, channel: channel, request: m.request}
}

var _ tview.Model[Model] = Model{}

func (Model) Init() tview.Cmd { return nil }

func (m Model) Update(msg tview.Msg) (Model, tview.Cmd) {
	switch msg := msg.(type) {
	case queryMsg:
		m.query, m.queryCursor = msg.query, msg.cursor
	case cursorMsg:
		m.cursor = int(msg)
		m.scroll.ScrollToTarget()
		return m, m.loadMore()
	case scrollMsg:
		m.scroll = viewport.ScrollState(msg)
	case submitMsg:
		return m, m.submit()
	case resultsMsg:
		m.setResults(msg)
	}
	return m, nil
}

// submit searches for the query, or shows the selected result if the results are for the query.
func (m *Model) submit() tview.Cmd {
	query := strings.TrimSpace(m.query)
	if query == "" || m.channel == nil {
		return nil
	}

	if query == m.searched && m.err == nil {
		if m.cursor < 0 || m.cursor >= len(m.results) {
			return nil
		}
		result := m.results[m.cursor]
		return cmd.Task(func() tview.Msg { return SelectedMsg{ChannelID: result.ChannelID, MessageID: result.ID} })
	}

	m.searched, m.results, m.total, m.err = query, nil, 0, nil
	m.cursor, m.scroll = 0, viewport.ScrollState{}
	return m.search(0)
}

// loadMore searches for the next page of results once the last one loaded is selected.
func (m *Model) loadMore() tview.Cmd {
	if m.loading || m.err != nil || m.cursor != len(m.results)-1 || uint(len(m.results)) >= m.total {
		return nil
	}
	return m.search(uint(len(m.results)))
}

// search asks Discord for the results of the query searched, starting at offset.
func (m *Model) search(offset uint) tview.Cmd {
	m.request++
	m.loading = true
	request, channel := m.request, *m.channel
	data := api.SearchData{Content: m.searched, ChannelID: channel.ID, Offset: offset}
	return cmd.Task(func() tview.Msg {
		var response api.SearchResponse
		var err error
		if channel.GuildID.IsValid() {
			response, err = m.state.Search(channel.GuildID, data)
		} else {
			response, err = m.state.SearchDirectMessages(data)
		}
		if err != nil {
			slog.Error("failed to search messages", "channel_id", channel.ID, "err", err)
			return resultsMsg{request: request, offset: offset, err: err}
		}

		// Each hit comes as a list, which once also held the messages around it, with the hit first.
		results := make([]discord.Message, 0, len(response.Messages))
		for _, hit := range response.Messages {
			if len(hit) != 0 {
				results = append(results, hit[0])
			}
		}
		return resultsMsg{request: request, offset: offset, results: results, total: response.TotalResults}
	})
}

// setResults adds the page of results msg has, unless a later search replaced the one it is for.
func (m *Model) setResults(msg resultsMsg) {
	if msg.request != m.request {
		return
	}

	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return
	}

	if msg.offset == 0 {
		m.results = msg.results
	} else {
		m.results = append(m.results, msg.results...)
	}
	m.total = msg.total
	// An empty page ends the results, whatever the total said.
	if len(msg.results) == 0 {
		m.total = uint(len(m.results))
	}
}

// status says what the search is doing or found, shown under the results.
func (m Model) status() string {
	switch {
	case m.loading && len(m.results) == 0:
		return "Searching..."
	case m.err != nil:
		return "Search failed"
	case m.searched == "":
		return "Type, then press enter to search"
	case m.total == 0:
		return "No results"
	default:
		return fmt.Sprintf("%d of %d", min(m.cursor+1, len(m.results)), m.total)
	}
}

// resultView shows who wrote message and when, above what it says.
func (m Model) resultView(message discord.Message) tview.Widget {
	theme := m.cfg.Theme.MessagesList
	when := message.Timestamp.Time().In(time.Local).Format(m.cfg.DateSeparator.Format + " " + m.cfg.Timestamps.Format)
	return text.Rich(
		text.Span{Text: m.authorName(message), Style: theme.AuthorStyle.Style},
		text.Span{Text: "  " + when + "\n", Style: theme.MessageStyle.Dim(true)},
		text.Span{Text: snippet(message), Style: theme.MessageStyle.Style},
	).Wrapping(text.WrappingNone)
}

// authorName returns the nickname of the author of message in the channel's guild, or their name.
func (m Model) authorName(message discord.Message) string {
	if m.channel != nil && m.channel.GuildID.IsValid() && !message.WebhookID.IsValid() {
		if member, err := m.state.Cabinet.Member(m.channel.GuildID, message.Author.ID); err == nil && member.Nick != "" {
			return member.Nick
		}
	}
	return message.Author.DisplayOrUsername()
}

// snippet returns the content of message on one line, or what it has instead of content.
func snippet(message discord.Message) string {
	content := strings.Join(strings.Fields(message.Content), " ")
	switch {
	case content != "":
		return content
	case len(message.Attachments) == 1:
		return "[1 attachment]"
	case len(message.Attachments) > 1:
		return fmt.Sprintf("[%d attachments]", len(message.Attachments))
	case len(message.Embeds) != 0:
		return "[embed]"
	default:
		return ""
	}
}

// View shows the query above the results, in a box titled after the channel searched.
func (m Model) View() tview.Widget {
	cfg := m.cfg
	bar := scrollbar.New().
		SymbolSet(cfg.UI.ScrollBar.SymbolSet.SymbolSet).
		Style(cfg.Theme.ScrollBar.TrackStyle.Style).
		ThumbStyle(cfg.Theme.ScrollBar.ThumbStyle.Style)
	selection := ui.SelectionKeybind(cfg.Keybinds.Picker.SelectionKeybinds)
	results := list.New(m.cursor, len(m.results), func(i int) tview.Widget { return m.resultView(m.results[i]) }).
		SelectedStyle(tcell.StyleDefault.Reverse(true)).
		Keybind(selection).
		Focused(true).
		OnChange(func(cursor int) tview.Msg { return cursorMsg(cursor) })
	resultsView := viewport.New(results, m.scroll).
		Target(results.Target(m.cursor)).
		ScrollBar(bar, cfg.UI.ScrollBar.Visibility.ScrollBarVisibility).
		OnChange(func(s *viewport.ScrollState) tview.Msg { return scrollMsg(*s) })

	title := "Search"
	if m.channel != nil {
		title += " " + ui.ChannelToString(*m.channel, cfg.Icons, m.state)
	}
	w := widget{query: m.query, queryCursor: m.queryCursor, results: resultsView, keybinds: cfg.Keybinds.Picker, selection: selection}
	return ui.Box(w, cfg, true).
		Title(ui.Label(title, cfg.Theme.Title, true)).
		Footer(ui.Label(m.status(), cfg.Theme.Footer, true))
}
