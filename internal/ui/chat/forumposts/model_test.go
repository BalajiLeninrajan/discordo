package forumposts

import (
	"errors"
	"slices"
	"testing"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
)

const (
	guildID = discord.GuildID(1)
	forumID = discord.ChannelID(10)
)

// newTestModel returns the posts of a forum read up to post 100, with tags, of which the second is for moderators,
// shown to its owner, or to a member who may only read it if reader. Posts 50 and 200 have read states, 200 with a
// mention.
func newTestModel(t *testing.T, flags discord.ChannelFlags, reader bool) Model {
	t.Helper()
	s := ningen.FromState(state.NewWithStore("", defaultstore.New()))
	me := discord.User{ID: 2}
	guild := discord.Guild{ID: guildID, Name: "guild", OwnerID: me.ID}
	if reader {
		guild.OwnerID = 3
	}
	forum := discord.Channel{
		ID: forumID, GuildID: guildID, Type: discord.GuildForum, Name: "forum", Flags: flags, LastMessageID: 100,
		AvailableTags: []discord.Tag{{ID: 1, Name: "question"}, {ID: 2, Name: "answered", Moderated: true}},
	}
	everyone := discord.Role{ID: discord.RoleID(guildID), Permissions: discord.PermissionViewChannel | discord.PermissionReadMessageHistory}
	if err := errors.Join(
		s.MyselfSet(me, false),
		s.GuildSet(&guild, false),
		s.MemberSet(guildID, &discord.Member{User: me}, false),
		s.RoleSet(guildID, &everyone, false),
		s.ChannelSet(&forum, false),
	); err != nil {
		t.Fatal(err)
	}
	s.State.Call(&gateway.ReadyEvent{User: me, ReadStates: []gateway.ReadState{
		{ChannelID: forumID, LastMessageID: 100},
		{ChannelID: 50, LastMessageID: 60},
		{ChannelID: 200, LastMessageID: 250, MentionCount: 2},
	}})

	cfg := config.Default()
	m := NewModel(&cfg, s)
	if m.Open(forum) == nil || !m.loading {
		t.Fatal("opening the posts does not load them")
	}
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

// loaded returns m with posts as the first page of those it loads, of more.
func loaded(m Model, more bool, posts ...discord.Channel) Model {
	m, _ = m.Update(resultsMsg{request: m.request, response: api.SearchThreadsResponse{Threads: posts, HasMore: more, TotalResults: len(posts)}})
	return m
}

func post(id discord.ChannelID, last discord.MessageID) discord.Channel {
	return discord.Channel{ID: id, Type: discord.GuildPublicThread, ParentID: forumID, LastMessageID: last, Name: "post"}
}

// sent returns what the task command sends.
func sent(t *testing.T, command tview.Cmd) tview.Msg {
	t.Helper()
	task, ok := command.(cmd.Task)
	if !ok {
		t.Fatalf("command = %T, want a task", command)
	}
	return task()
}

func TestModelUnread(t *testing.T) {
	m := newTestModel(t, 0, false)
	m.joined = map[discord.ChannelID]discord.ThreadMember{50: {ID: 50}}
	for _, test := range []struct {
		name string
		post discord.Channel
		want ningen.UnreadIndication
	}{
		{"new", post(300, 300), ningen.ChannelUnread},
		{"older than the forum was read", post(90, 90), ningen.ChannelRead},
		{"joined with new messages", post(50, 70), ningen.ChannelUnread},
		{"joined and read", post(50, 60), ningen.ChannelRead},
		{"mentioned", post(200, 250), ningen.ChannelMentioned},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := m.unread(test.post); got != test.want {
				t.Errorf("unread = %v, want %v", got, test.want)
			}
		})
	}

	t.Run("not joined", func(t *testing.T) {
		m.joined = nil
		if got := m.unread(post(50, 70)); got != ningen.ChannelRead {
			t.Errorf("unread = %v, want read", got)
		}
	})

	// Showing the forum reads it, but what was new stays so.
	t.Run("after the forum is read", func(t *testing.T) {
		// An ack from Discord, as another client sends, rather than MarkRead, which would send a request.
		m.state.State.Call(&gateway.MessageAckEvent{ChannelID: forumID, MessageID: 300})
		if state := m.state.ReadState.ReadState(forumID); state == nil || state.LastMessageID != 300 {
			t.Fatal("the forum is not read")
		}
		if got := m.unread(post(300, 300)); got != ningen.ChannelUnread {
			t.Errorf("unread = %v, want unread", got)
		}
	})
}

func TestModelResults(t *testing.T) {
	t.Run("drops replaced searches", func(t *testing.T) {
		m := newTestModel(t, 0, false)
		m, _ = m.Update(resultsMsg{request: m.request - 1, response: api.SearchThreadsResponse{Threads: []discord.Channel{post(300, 300)}}})
		if len(m.posts) != 0 || !m.loading {
			t.Fatalf("posts = %d, loading = %v", len(m.posts), m.loading)
		}
	})

	t.Run("reads the forum up to its newest post", func(t *testing.T) {
		m := newTestModel(t, 0, false)
		_, command := m.Update(resultsMsg{request: m.request, response: api.SearchThreadsResponse{Threads: []discord.Channel{post(300, 300)}, TotalResults: 1}})
		if command == nil {
			t.Error("the forum is not read")
		}
		_, command = m.Update(resultsMsg{request: m.request, response: api.SearchThreadsResponse{Threads: []discord.Channel{post(90, 90)}, TotalResults: 1}})
		if command != nil {
			t.Error("a forum already read is read again")
		}
	})

	t.Run("loads more at the last post", func(t *testing.T) {
		m := loaded(newTestModel(t, 0, false), true, post(300, 300), post(90, 90))
		if m, command := update(m, cursorMsg(1)); command == nil || !m.loading {
			t.Fatal("selecting the last post does not load more")
		}
		if _, command := update(loaded(m, false, post(300, 300), post(90, 90)), cursorMsg(1)); command != nil {
			t.Fatal("more is loaded when there is none")
		}
	})
}

func TestModelSubmit(t *testing.T) {
	t.Run("opens the selected post", func(t *testing.T) {
		m := loaded(newTestModel(t, 0, false), false, post(300, 300), post(90, 90))
		m.joined = map[discord.ChannelID]discord.ThreadMember{90: {ID: 90}}
		m, command := update(m, cursorMsg(1), submitMsg{})
		msg, ok := sent(t, command).(SelectedMsg)
		if !ok || msg.Post.ID != 90 || msg.Post.GuildID != guildID || msg.Post.ThreadMember == nil {
			t.Fatalf("msg = %#v, want post 90 in the guild, joined", msg)
		}
	})

	t.Run("searches for a new query", func(t *testing.T) {
		m := loaded(newTestModel(t, 0, false), false, post(300, 300))
		m, command := update(m, queryMsg{query: " help ", cursor: 6}, submitMsg{})
		if command == nil || !m.loading || m.searched != "help" || len(m.posts) != 0 {
			t.Fatalf("loading = %v, searched = %q, posts = %d", m.loading, m.searched, len(m.posts))
		}
	})

	t.Run("cancels", func(t *testing.T) {
		if _, ok := sent(t, func() tview.Cmd { _, c := newTestModel(t, 0, false).Update(cancelMsg{}); return c }()).(CancelMsg); !ok {
			t.Fatal("cancelling does not close the posts")
		}
	})
}

func TestModelNewPost(t *testing.T) {
	t.Run("needs a title", func(t *testing.T) {
		m, command := update(newTestModel(t, 0, false), queryMsg{query: "  "}, newPostMsg{})
		if command != nil || m.problem == "" || m.picking {
			t.Fatalf("command = %v, problem = %q", command, m.problem)
		}
	})

	t.Run("needs permission", func(t *testing.T) {
		m, command := update(newTestModel(t, 0, true), queryMsg{query: "title"}, newPostMsg{})
		if command != nil || m.problem == "" {
			t.Fatalf("command = %v, problem = %q", command, m.problem)
		}
	})

	t.Run("without a tag", func(t *testing.T) {
		m, command := update(newTestModel(t, 0, false), queryMsg{query: "title"}, newPostMsg{})
		if command != nil || !m.picking || m.tagChoices() != 3 {
			t.Fatalf("picking = %v, choices = %d", m.picking, m.tagChoices())
		}
		msg, ok := sent(t, func() tview.Cmd { _, c := update(m, submitMsg{}); return c }()).(NewPostMsg)
		if !ok || msg.Title != "title" || msg.Tags != nil {
			t.Fatalf("msg = %#v, want a post titled title without tags", msg)
		}
	})

	t.Run("with a tag", func(t *testing.T) {
		m, command := update(newTestModel(t, 0, false), queryMsg{query: "title"}, newPostMsg{}, cursorMsg(2), submitMsg{})
		msg, ok := sent(t, command).(NewPostMsg)
		if !ok || !slices.Equal(msg.Tags, []discord.TagID{2}) || m.picking {
			t.Fatalf("msg = %#v, picking = %v, want the moderators' tag", msg, m.picking)
		}
	})

	t.Run("a required tag", func(t *testing.T) {
		m, _ := update(newTestModel(t, discord.ThreadRequireTag, false), queryMsg{query: "title"}, newPostMsg{})
		if m.tagChoices() != 2 {
			t.Fatalf("choices = %d, want the tags without none", m.tagChoices())
		}
		_, command := update(m, submitMsg{})
		if msg, ok := sent(t, command).(NewPostMsg); !ok || !slices.Equal(msg.Tags, []discord.TagID{1}) {
			t.Fatalf("msg = %#v, want the first tag", msg)
		}
	})

	t.Run("goes back from the tags", func(t *testing.T) {
		m, command := update(newTestModel(t, 0, false), queryMsg{query: "title"}, newPostMsg{}, cancelMsg{})
		if command != nil || m.picking {
			t.Fatalf("command = %v, picking = %v", command, m.picking)
		}
	})
}
