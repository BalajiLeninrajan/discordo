// Package forumposts lists the posts of a forum, searches them by title, and starts new ones.
package forumposts

import (
	"cmp"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
	"github.com/ayn2op/tview/help"
	"github.com/ayn2op/tview/keybind"
	"github.com/ayn2op/tview/list"
	"github.com/ayn2op/tview/scrollbar"
	"github.com/ayn2op/tview/text"
	"github.com/ayn2op/tview/viewport"
	"github.com/gdamore/tcell/v3"
)

const (
	// pageSize is the most posts Discord returns at once.
	pageSize = 25
	// maxTitleLength is the longest title Discord takes.
	maxTitleLength = 100
	// indexNotReady is the code of Discord's answer to a search of a forum it has not indexed yet.
	indexNotReady = 110000
	// maxRetries is how many times a search waits for the index before it fails.
	maxRetries = 3
)

type Model struct {
	cfg   *config.Config
	state *ningen.State

	// forum is the forum shown, or nil before one is.
	forum *discord.Channel
	// seen maps each forum shown to the message it was read up to before it was first shown. Showing a forum reads
	// it, as Discord does, and the posts newer than that stay marked new until they are opened.
	seen map[discord.ChannelID]discord.MessageID

	// query is what is typed, with the cursor at the byte offset queryCursor in it.
	query       string
	queryCursor int
	// searched is the query the posts are for.
	searched string
	// posts are those loaded so far, of total, and joined the ones the user is a member of.
	posts   []discord.Channel
	joined  map[discord.ChannelID]discord.ThreadMember
	total   int
	hasMore bool
	// cursor is the selected post and scroll how far the posts are scrolled.
	cursor int
	scroll viewport.ScrollState
	// loading reports whether a search is on its way, and err why the last one failed.
	loading bool
	err     error
	// request numbers the searches, so that the results of one replaced by another are dropped.
	request int

	// picking reports whether a tag of a new post titled title is picked, and tag is the one selected.
	picking bool
	title   string
	tag     int
	// problem is why a post cannot be started, or "".
	problem string
}

func NewModel(cfg *config.Config, state *ningen.State) Model {
	return Model{cfg: cfg, state: state, seen: make(map[discord.ChannelID]discord.MessageID)}
}

// Open shows the posts of forum, keeping the query if it is the forum shown last.
func (m *Model) Open(forum discord.Channel) tview.Cmd {
	if _, ok := m.seen[forum.ID]; !ok {
		var read discord.MessageID
		if state := m.state.ReadState.ReadState(forum.ID); state != nil {
			read = state.LastMessageID
		}
		m.seen[forum.ID] = read
	}

	query, cursor := m.query, m.queryCursor
	if m.forum == nil || m.forum.ID != forum.ID {
		query, cursor = "", 0
	}
	*m = Model{cfg: m.cfg, state: m.state, seen: m.seen, forum: &forum, query: query, queryCursor: cursor, request: m.request}
	m.searched = strings.TrimSpace(query)
	return m.search(0)
}

var _ tview.Model[Model] = Model{}

func (Model) Init() tview.Cmd { return nil }

func (m Model) Update(msg tview.Msg) (Model, tview.Cmd) {
	switch msg := msg.(type) {
	case queryMsg:
		if msg.query != m.query {
			m.picking, m.problem = false, ""
		}
		m.query, m.queryCursor = msg.query, msg.cursor
	case cursorMsg:
		if m.picking {
			m.tag = int(msg)
			return m, nil
		}
		m.cursor = int(msg)
		m.scroll.ScrollToTarget()
		return m, m.loadMore()
	case scrollMsg:
		m.scroll = viewport.ScrollState(msg)
	case submitMsg:
		return m, m.submit()
	case cancelMsg:
		if m.picking {
			m.picking = false
			return m, nil
		}
		return m, send(CancelMsg{})
	case newPostMsg:
		return m, m.newPost()
	case resultsMsg:
		return m, m.setResults(msg)
	}
	return m, nil
}

func send(msg tview.Msg) tview.Cmd {
	return cmd.Task(func() tview.Msg { return msg })
}

// submit starts the post with the selected tag while one is picked. Otherwise it opens the selected post if the
// posts are for the query, or searches for the query.
func (m *Model) submit() tview.Cmd {
	if m.forum == nil {
		return nil
	}
	if m.picking {
		return m.startPost()
	}

	query := strings.TrimSpace(m.query)
	if query == m.searched && m.err == nil {
		if m.cursor < 0 || m.cursor >= len(m.posts) {
			return nil
		}
		post := m.posts[m.cursor]
		post.GuildID = cmp.Or(post.GuildID, m.forum.GuildID)
		if member, ok := m.joined[post.ID]; ok {
			post.ThreadMember = &member
		}
		return send(SelectedMsg{Post: post})
	}

	m.searched, m.posts, m.joined, m.total, m.hasMore, m.err = query, nil, nil, 0, false, nil
	m.cursor, m.scroll = 0, viewport.ScrollState{}
	return m.search(0)
}

// newPost starts a post titled with the query, after a tag is picked if the forum has tags the user may apply.
func (m *Model) newPost() tview.Cmd {
	if m.forum == nil {
		return nil
	}
	title := strings.TrimSpace(m.query)
	switch {
	case !m.state.HasPermissions(m.forum.ID, discord.PermissionSendMessages):
		m.problem = "You may not post here"
		return nil
	case title == "":
		m.problem = "Type the title of the post first"
		return nil
	case len([]rune(title)) > maxTitleLength:
		m.problem = fmt.Sprintf("Titles are at most %d characters", maxTitleLength)
		return nil
	}

	m.title, m.problem = title, ""
	if len(m.tags()) == 0 {
		return m.startPost()
	}
	m.picking, m.tag = true, 0
	return nil
}

// startPost asks for the first message of the post, with the selected tag if one is picked.
func (m *Model) startPost() tview.Cmd {
	var tags []discord.TagID
	if m.picking {
		if tag, ok := m.selectedTag(); ok {
			tags = []discord.TagID{tag.ID}
		}
	}
	m.picking = false
	return send(NewPostMsg{Forum: *m.forum, Title: m.title, Tags: tags})
}

// tagRequired reports whether a post in the forum must have a tag.
func (m Model) tagRequired() bool {
	return m.forum.Flags&discord.ThreadRequireTag != 0
}

// tags returns the tags the user may apply to a post, which are not those only moderators may, unless they are one.
func (m Model) tags() []discord.Tag {
	moderator := m.state.HasPermissions(m.forum.ID, discord.PermissionManageThreads)
	var tags []discord.Tag
	for _, tag := range m.forum.AvailableTags {
		if !tag.Moderated || moderator {
			tags = append(tags, tag)
		}
	}
	return tags
}

// tagChoices returns the number of choices while a tag is picked, which start with none unless one is required.
func (m Model) tagChoices() int {
	if m.tagRequired() {
		return len(m.tags())
	}
	return len(m.tags()) + 1
}

// selectedTag returns the tag selected, or false if no tag is.
func (m Model) selectedTag() (discord.Tag, bool) {
	index := m.tag
	if !m.tagRequired() {
		index--
	}
	tags := m.tags()
	if index < 0 || index >= len(tags) {
		return discord.Tag{}, false
	}
	return tags[index], true
}

// loadMore searches for the next page of posts once the last one loaded is selected.
func (m *Model) loadMore() tview.Cmd {
	if m.loading || m.err != nil || !m.hasMore || m.cursor != len(m.posts)-1 {
		return nil
	}
	return m.search(len(m.posts))
}

// sortBy returns how the forum sorts its posts.
func sortBy(forum discord.Channel) api.ThreadSortBy {
	if order := forum.DefaultSoftOrder; order != nil && *order == discord.SoftOrderTypeCreationDate {
		return api.CreationTime
	}
	return api.LastMessageTime
}

// searchResponse is the answer to a search, which is one of not being ready yet if Discord has not indexed the forum.
type searchResponse struct {
	api.SearchThreadsResponse
	Code       int     `json:"code"`
	RetryAfter float64 `json:"retry_after"`
}

// search asks Discord for the posts matching the query searched, starting at offset, active or archived.
func (m *Model) search(offset int) tview.Cmd {
	m.request++
	m.loading = true
	request, forum, client := m.request, *m.forum, m.state.Client
	data := api.SearchThreadsData{
		Name:      m.searched,
		SortBy:    sortBy(forum),
		SortOrder: api.SortDescending,
		Limit:     pageSize,
		Offset:    offset,
	}
	if data.Name != "" {
		data.SortBy = api.Relevance
	}
	return cmd.Task(func() tview.Msg {
		endpoint := api.EndpointChannels + forum.ID.String() + "/threads/search"
		for retry := 0; ; retry++ {
			response, err := client.RequestJSON[searchResponse]("GET", endpoint, httputil.WithSchema(client, data))
			if err == nil && response.Code == indexNotReady {
				if retry < maxRetries {
					time.Sleep(time.Duration(max(response.RetryAfter, 1) * float64(time.Second)))
					continue
				}
				err = fmt.Errorf("discord has not indexed the forum yet")
			}
			if err != nil {
				slog.Error("failed to search forum posts", "channel_id", forum.ID, "err", err)
			}
			return resultsMsg{request: request, offset: offset, response: response.SearchThreadsResponse, err: err}
		}
	})
}

// setResults adds the page of posts msg has, unless a later search replaced the one it is for. The first page reads
// the forum up to its newest post.
func (m *Model) setResults(msg resultsMsg) tview.Cmd {
	if msg.request != m.request {
		return nil
	}

	m.loading = false
	if msg.err != nil {
		m.err = msg.err
		return nil
	}

	response := msg.response
	if msg.offset == 0 {
		m.posts, m.joined = nil, make(map[discord.ChannelID]discord.ThreadMember)
	}
	m.posts = append(m.posts, response.Threads...)
	for _, member := range response.Members {
		m.joined[member.ID] = member
	}
	m.total, m.hasMore = max(response.TotalResults, len(m.posts)), response.HasMore && len(response.Threads) != 0
	if msg.offset == 0 {
		return m.readForum()
	}
	return nil
}

// readForum marks the forum read up to its newest post, of those Discord knows of and those shown.
func (m Model) readForum() tview.Cmd {
	newest := m.state.LastMessage(m.forum.ID)
	for _, post := range m.posts {
		newest = max(newest, discord.MessageID(post.ID))
	}

	state := m.state.ReadState.ReadState(m.forum.ID)
	if !newest.IsValid() || (state != nil && state.LastMessageID >= newest) {
		return nil
	}

	forumID := m.forum.ID
	return cmd.Task(func() tview.Msg {
		m.state.ReadState.MarkRead(forumID, newest)
		return nil
	})
}

// unread returns whether post has messages or mentions the user has not read, which a post they are not in has only
// if it is new: posted after they last read the forum before it was shown, and not opened since.
func (m Model) unread(post discord.Channel) ningen.UnreadIndication {
	state := m.state.ReadState.ReadState(post.ID)
	if state == nil {
		if seen := m.seen[m.forum.ID]; seen.IsValid() && discord.MessageID(post.ID) > seen {
			return ningen.ChannelUnread
		}
		return ningen.ChannelRead
	}

	if state.MentionCount > 0 {
		return ningen.ChannelMentioned
	}
	member, joined := m.joined[post.ID]
	if !joined || member.Muted || !post.LastMessageID.IsValid() || state.LastMessageID >= post.LastMessageID {
		return ningen.ChannelRead
	}
	return ningen.ChannelUnread
}

// status says what the posts are doing or found, shown under them.
func (m Model) status() string {
	switch {
	case m.problem != "":
		return m.problem
	case m.picking && m.tagRequired():
		return "Pick a tag for the post"
	case m.picking:
		return "Pick a tag for the post, if any"
	case m.loading && len(m.posts) == 0:
		return "Loading..."
	case m.err != nil:
		return "Failed to load the posts"
	case m.total == 0 && m.searched != "":
		return "No posts found"
	case m.total == 0:
		return "No posts"
	default:
		return fmt.Sprintf("%d of %d", min(m.cursor+1, len(m.posts)), m.total)
	}
}

// postView shows the title of post, styled as unread if it is, above its tags, number of messages and last activity.
func (m Model) postView(post discord.Channel) tview.Widget {
	unread := m.unread(post)
	title := []text.Span{{Text: post.Name, Style: ui.UnreadStyle(unread)}}
	if state := m.state.ReadState.ReadState(post.ID); state != nil && state.MentionCount > 0 {
		title = append(title, text.Span{Text: fmt.Sprintf(" (%d)", state.MentionCount), Style: ui.UnreadStyle(unread)})
	} else if unread != ningen.ChannelRead && state == nil {
		title = append(title, text.Span{Text: " new", Style: tcell.StyleDefault.Bold(true).Italic(true)})
	}

	var details []string
	if post.Flags&discord.PinnedThread != 0 {
		details = append(details, "pinned")
	}
	if tags := m.tagNames(post.AppliedTags); tags != "" {
		details = append(details, tags)
	}
	details = append(details, plural(post.MessageCount, "message"))
	last := post.ID.Time()
	if post.LastMessageID.IsValid() {
		last = post.LastMessageID.Time()
	}
	details = append(details, last.In(time.Local).Format(m.cfg.DateSeparator.Format+" "+m.cfg.Timestamps.Format))

	spans := append(title, text.Span{Text: "\n" + strings.Join(details, " · "), Style: tcell.StyleDefault.Dim(true)})
	return text.Rich(spans...).Wrapping(text.WrappingNone)
}

// tagNames returns the names of the tags of the forum with ids.
func (m Model) tagNames(ids []discord.TagID) string {
	var names []string
	for _, id := range ids {
		for _, tag := range m.forum.AvailableTags {
			if tag.ID == id {
				names = append(names, tagLabel(tag))
			}
		}
	}
	return strings.Join(names, ", ")
}

// tagLabel returns the name of tag after its emoji, if it is not a custom one.
func tagLabel(tag discord.Tag) string {
	if emoji := tag.EmojiName; emoji != nil && *emoji != "" {
		return *emoji + " " + tag.Name
	}
	return tag.Name
}

func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// tagView shows the tag choice at index, the first of which is none unless a tag is required.
func (m Model) tagView(index int) tview.Widget {
	if !m.tagRequired() {
		if index == 0 {
			return text.New("No tag").Wrapping(text.WrappingNone)
		}
		index--
	}
	return text.New(tagLabel(m.tags()[index])).Wrapping(text.WrappingNone)
}

var _ help.KeyMap = Model{}

func (m Model) ShortHelp() []keybind.Keybind {
	picker := m.cfg.Keybinds.Picker
	return []keybind.Keybind{picker.SelectUp.Keybind, picker.SelectDown.Keybind, m.selectKeybind(), picker.Cancel.Keybind, m.cfg.Keybinds.Forum.NewPost.Keybind}
}

func (m Model) FullHelp() [][]keybind.Keybind {
	picker := m.cfg.Keybinds.Picker
	return [][]keybind.Keybind{
		{picker.SelectUp.Keybind, picker.SelectDown.Keybind, picker.SelectTop.Keybind, picker.SelectBottom.Keybind},
		{m.selectKeybind(), picker.Cancel.Keybind, m.cfg.Keybinds.Forum.NewPost.Keybind},
	}
}

// selectKeybind returns the select keybind, described as what it does now.
func (m Model) selectKeybind() keybind.Keybind {
	selectKey := m.cfg.Keybinds.Picker.Select.Keybind
	desc := "open"
	switch {
	case m.picking:
		desc = "pick tag"
	case strings.TrimSpace(m.query) != m.searched || m.err != nil:
		desc = "search"
	}
	return selectKey.WithHelp(selectKey.Help().Key, desc)
}

// View shows the query above the posts, or the tags while one is picked, in a box titled after the forum.
func (m Model) View() tview.Widget {
	cfg := m.cfg
	selection := ui.SelectionKeybind(cfg.Keybinds.Picker.SelectionKeybinds)
	w := widget{query: m.query, queryCursor: m.queryCursor, keybinds: cfg.Keybinds.Picker, newPost: cfg.Keybinds.Forum.NewPost.Keybind, selection: selection}

	title := "Posts"
	if m.forum != nil {
		title = ui.ChannelToString(*m.forum, cfg.Icons, m.state)
	}
	if m.picking {
		title = "New post: " + m.title
	}

	if m.picking {
		l := list.New(m.tag, m.tagChoices(), m.tagView).
			SelectedStyle(tcell.StyleDefault.Reverse(true)).
			Keybind(selection).
			Focused(true).
			OnChange(func(cursor int) tview.Msg { return cursorMsg(cursor) })
		w.list = viewport.New(l, viewport.ScrollState{}).Target(l.Target(m.tag))
	} else {
		bar := scrollbar.New().
			SymbolSet(cfg.UI.ScrollBar.SymbolSet.SymbolSet).
			Style(cfg.Theme.ScrollBar.TrackStyle.Style).
			ThumbStyle(cfg.Theme.ScrollBar.ThumbStyle.Style)
		posts := list.New(m.cursor, len(m.posts), func(i int) tview.Widget { return m.postView(m.posts[i]) }).
			SelectedStyle(tcell.StyleDefault.Reverse(true)).
			Keybind(selection).
			Focused(true).
			OnChange(func(cursor int) tview.Msg { return cursorMsg(cursor) })
		w.list = viewport.New(posts, m.scroll).
			Target(posts.Target(m.cursor)).
			ScrollBar(bar, cfg.UI.ScrollBar.Visibility.ScrollBarVisibility).
			OnChange(func(s *viewport.ScrollState) tview.Msg { return scrollMsg(*s) })
	}

	return ui.Box(w, cfg, true).
		Title(ui.Label(title, cfg.Theme.Title, true)).
		Footer(ui.Label(m.status(), cfg.Theme.Footer, true))
}
