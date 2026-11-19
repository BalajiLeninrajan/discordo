package guildstree

import (
	"errors"
	"slices"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/ningen/v3/states/read"
	"github.com/ayn2op/tview/cmd"
	"github.com/ayn2op/tview/tree"
)

func TestModelMoveDMToFront(t *testing.T) {
	t.Run("moves rendered channel to front", func(t *testing.T) {
		firstID := discord.ChannelID(1)
		secondID := discord.ChannelID(2)
		thirdID := discord.ChannelID(3)

		first := tree.NewNode("first").SetReference(firstID)
		second := tree.NewNode("second").SetReference(secondID)
		third := tree.NewNode("third").SetReference(thirdID)
		dmRoot := tree.NewNode("Direct Messages").SetChildren([]*tree.Node{first, second, third})
		m := Model{
			dmRootNode: dmRoot,
			channelNodes: map[discord.ChannelID]*tree.Node{
				firstID:  first,
				secondID: second,
				thirdID:  third,
			},
		}

		m.moveDMToFront(thirdID)

		want := []*tree.Node{third, first, second}
		if !slices.Equal(dmRoot.Children(), want) {
			t.Fatalf("children = %v, want %v", dmRoot.Children(), want)
		}
	})

	t.Run("ignores unrendered channel", func(t *testing.T) {
		firstID := discord.ChannelID(1)
		first := tree.NewNode("first").SetReference(firstID)
		dmRoot := tree.NewNode("Direct Messages").SetChildren([]*tree.Node{first})
		m := Model{
			dmRootNode:   dmRoot,
			channelNodes: map[discord.ChannelID]*tree.Node{},
		}

		m.moveDMToFront(discord.ChannelID(2))

		children := dmRoot.Children()
		if len(children) != 1 || children[0] != first {
			t.Fatalf("children = %v, want the original DM node", children)
		}
	})
}

func TestAdjacentNode(t *testing.T) {
	a, b, c := tree.NewNode("a"), tree.NewNode("b"), tree.NewNode("c")
	root := tree.NewNode("").SetChildren([]*tree.Node{a, tree.NewNode("guild").AddChild(b), c})
	match := func(n *tree.Node) bool { return n == a || n == b }
	for _, test := range []struct {
		name          string
		current       *tree.Node
		after, before *tree.Node
	}{
		{"none", nil, a, b},
		{"a", a, b, b},
		{"b", b, a, a},
		{"c", c, a, b},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Run("after", func(t *testing.T) {
				if got := adjacentNode(root, test.current, match, false); got != test.after {
					t.Errorf("got %v, want %v", got, test.after)
				}
			})
			t.Run("before", func(t *testing.T) {
				if got := adjacentNode(root, test.current, match, true); got != test.before {
					t.Errorf("got %v, want %v", got, test.before)
				}
			})
		})
	}
	t.Run("no match", func(t *testing.T) {
		if got := adjacentNode(root, a, func(*tree.Node) bool { return false }, false); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestMentionsLabel(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		want  string
	}{
		{"none", 0, ""},
		{"some", 5, " (5)"},
		{"most counted", maxMentions, " (99)"},
		{"more", maxMentions + 1, " (99+)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := mentionsLabel(test.count); got != test.want {
				t.Errorf("mentionsLabel(%d) = %q, want %q", test.count, got, test.want)
			}
		})
	}
}

func TestModelMentions(t *testing.T) {
	s := ningen.FromState(state.NewWithStore("", defaultstore.New()))
	s.State.Call(&gateway.ReadyEvent{ReadStates: []gateway.ReadState{
		{ChannelID: 1, LastMessageID: 10, MentionCount: 2},
		{ChannelID: 2, LastMessageID: 20, MentionCount: 3},
	}})
	m := Model{state: s}
	channels := []discord.Channel{{ID: 1}, {ID: 2}, {ID: 3}}
	if got := m.mentions(channels...); got != 5 {
		t.Errorf("mentions = %d, want 5", got)
	}
}

func TestModelRefreshReadState(t *testing.T) {
	t.Run("keeps the name of a channel sharing its guild's ID", func(t *testing.T) {
		s := ningen.FromState(state.NewWithStore("", defaultstore.New()))
		me := discord.User{ID: 2}
		guild := discord.Guild{ID: 1, Name: "guild", OwnerID: me.ID}
		channel := discord.Channel{ID: 1, GuildID: guild.ID, Type: discord.GuildText, Name: "general"}
		if err := errors.Join(
			s.MyselfSet(me, false),
			s.GuildSet(&guild, false),
			s.MemberSet(guild.ID, &discord.Member{User: me}, false),
			s.RoleSet(guild.ID, &discord.Role{ID: discord.RoleID(guild.ID)}, false),
			s.ChannelSet(&channel, false),
		); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		m := NewModel(&cfg, s)
		m.createGuildNode(m.root, guild)
		guildNode := m.root.Children()[0]
		m.createChannelNode(guildNode, channel)
		channelNode := guildNode.Children()[0]

		m.refreshReadState(&read.UpdateEvent{ReadState: gateway.ReadState{ChannelID: 2}, GuildID: guild.ID})

		if got, want := guildNode.Spans()[0].Text, m.guildText(guild); got != want {
			t.Errorf("guild node text = %q, want %q", got, want)
		}
		if got, want := channelNode.Spans()[0].Text, m.channelText(channel); got != want {
			t.Errorf("channel node text = %q, want %q", got, want)
		}
	})
}

func TestModelUnreadChannels(t *testing.T) {
	s := ningen.FromState(state.NewWithStore("", defaultstore.New()))
	me := discord.User{ID: 2}
	guild := discord.Guild{ID: 1, Name: "guild", OwnerID: me.ID}
	category := discord.Channel{ID: 10, GuildID: guild.ID, Type: discord.GuildCategory, Name: "category"}
	unread := discord.Channel{ID: 11, GuildID: guild.ID, Type: discord.GuildText, Name: "unread", ParentID: category.ID, LastMessageID: 200}
	read := discord.Channel{ID: 12, GuildID: guild.ID, Type: discord.GuildText, Name: "read", ParentID: category.ID, LastMessageID: 200}
	mentioned := discord.Channel{ID: 13, GuildID: guild.ID, Type: discord.GuildText, Name: "mentioned", LastMessageID: 200}
	// A channel without a read state was never opened, and is left alone.
	unseen := discord.Channel{ID: 14, GuildID: guild.ID, Type: discord.GuildText, Name: "unseen", LastMessageID: 200}
	dm := discord.Channel{ID: 20, Type: discord.DirectMessage, LastMessageID: 200, DMRecipients: []discord.User{{ID: 3}}}
	other := discord.Guild{ID: 4, Name: "other", OwnerID: me.ID}
	otherUnread := discord.Channel{ID: 30, GuildID: other.ID, Type: discord.GuildText, Name: "other unread", LastMessageID: 200}
	if err := errors.Join(
		s.MyselfSet(me, false),
		s.GuildSet(&guild, false),
		s.MemberSet(guild.ID, &discord.Member{User: me}, false),
		s.RoleSet(guild.ID, &discord.Role{ID: discord.RoleID(guild.ID)}, false),
		s.GuildSet(&other, false),
		s.MemberSet(other.ID, &discord.Member{User: me}, false),
		s.RoleSet(other.ID, &discord.Role{ID: discord.RoleID(other.ID)}, false),
		s.ChannelSet(&otherUnread, false),
		s.ChannelSet(&category, false),
		s.ChannelSet(&unread, false),
		s.ChannelSet(&read, false),
		s.ChannelSet(&mentioned, false),
		s.ChannelSet(&unseen, false),
		s.ChannelSet(&dm, false),
	); err != nil {
		t.Fatal(err)
	}
	s.State.Call(&gateway.ReadyEvent{User: me, ReadStates: []gateway.ReadState{
		{ChannelID: unread.ID, LastMessageID: 100},
		{ChannelID: read.ID, LastMessageID: 200},
		{ChannelID: mentioned.ID, LastMessageID: 200, MentionCount: 1},
		{ChannelID: dm.ID, LastMessageID: 100},
		{ChannelID: otherUnread.ID, LastMessageID: 100},
	}})

	cfg := config.Default()
	m := NewModel(&cfg, s)
	for _, test := range []struct {
		name string
		ref  any
		want []discord.ChannelID
	}{
		{"channel", unread.ID, []discord.ChannelID{unread.ID}},
		{"read channel", read.ID, nil},
		{"mentioned channel", mentioned.ID, []discord.ChannelID{mentioned.ID}},
		{"unseen channel", unseen.ID, nil},
		{"category", category.ID, []discord.ChannelID{unread.ID}},
		{"guild", guild.ID, []discord.ChannelID{unread.ID, mentioned.ID}},
		{"direct messages", dmNode{}, []discord.ChannelID{dm.ID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got []discord.ChannelID
			for _, channel := range m.unreadChannels(tree.NewNode("").SetReference(test.ref)) {
				got = append(got, channel.ID)
			}
			slices.Sort(got)
			if !slices.Equal(got, test.want) {
				t.Errorf("unreadChannels = %v, want %v", got, test.want)
			}
		})
	}

	t.Run("folder", func(t *testing.T) {
		folder := tree.NewNode("").SetReference(folderNode{})
		folder.AddChild(tree.NewNode("").SetReference(guild.ID))
		folder.AddChild(tree.NewNode("").SetReference(other.ID))
		var got []discord.ChannelID
		for _, channel := range m.unreadChannels(folder) {
			got = append(got, channel.ID)
		}
		slices.Sort(got)
		if want := []discord.ChannelID{unread.ID, mentioned.ID, otherUnread.ID}; !slices.Equal(got, want) {
			t.Errorf("unreadChannels = %v, want %v", got, want)
		}
	})

	if got := m.unreadChannels(nil); got != nil {
		t.Errorf("unreadChannels(nil) = %v, want none", got)
	}

	if got := m.categoryUnread(category); got != ningen.ChannelUnread {
		t.Errorf("categoryUnread = %v, want %v", got, ningen.ChannelUnread)
	}
	if got, want := m.channelNodeStyle(category), ui.UnreadStyle(ningen.ChannelUnread); got != want {
		t.Errorf("category style = %v, want %v", got, want)
	}
	empty := discord.Channel{ID: 15, GuildID: guild.ID, Type: discord.GuildCategory, Name: "empty"}
	if got := m.categoryUnread(empty); got != ningen.ChannelRead {
		t.Errorf("categoryUnread of an empty category = %v, want %v", got, ningen.ChannelRead)
	}
}

func TestModelChannelUnreadDirectMessages(t *testing.T) {
	s := ningen.FromState(state.NewWithStore("", defaultstore.New()))
	me := discord.User{ID: 2}
	friend := discord.User{ID: 3}
	read := discord.Channel{ID: 20, Type: discord.DirectMessage, LastMessageID: 200, DMRecipients: []discord.User{friend}}
	unread := discord.Channel{ID: 21, Type: discord.GroupDM, LastMessageID: 200, DMRecipients: []discord.User{friend, {ID: 4}}}
	if err := errors.Join(
		s.MyselfSet(me, false),
		s.ChannelSet(&read, false),
		s.ChannelSet(&unread, false),
	); err != nil {
		t.Fatal(err)
	}
	s.State.Call(&gateway.ReadyEvent{User: me, ReadStates: []gateway.ReadState{
		{ChannelID: read.ID, LastMessageID: 200},
		{ChannelID: unread.ID, LastMessageID: 100},
	}})

	cfg := config.Default()
	m := NewModel(&cfg, s)
	if got := m.channelUnread(read.ID); got != ningen.ChannelRead {
		t.Errorf("channelUnread(read) = %v, want ChannelRead", got)
	}
	if got := m.channelUnread(unread.ID); got != ningen.ChannelUnread {
		t.Errorf("channelUnread(unread) = %v, want ChannelUnread", got)
	}
	if got := m.dmUnread(); got != ningen.ChannelUnread {
		t.Errorf("dmUnread() = %v, want ChannelUnread", got)
	}

	// A message that arrives in the read direct message makes it unread.
	s.State.Call(&gateway.MessageCreateEvent{Message: discord.Message{ID: 300, ChannelID: read.ID, Author: friend}})
	if got := m.channelUnread(read.ID); got != ningen.ChannelUnread {
		t.Errorf("channelUnread(read) after a new message = %v, want ChannelUnread", got)
	}

	// An ack from Discord, as another client sends, rather than MarkRead, which would send a request.
	s.State.Call(&gateway.MessageAckEvent{ChannelID: unread.ID, MessageID: 200})
	if got := m.channelUnread(unread.ID); got != ningen.ChannelRead {
		t.Errorf("channelUnread(unread) after marking it read = %v, want ChannelRead", got)
	}
}

// newForumModel returns a model with the guild's channels shown: a forum read up to post 100, in a category, with an
// old post.
func newForumModel(t *testing.T) (Model, *ningen.State, discord.Channel) {
	t.Helper()
	s := ningen.FromState(state.NewWithStore("", defaultstore.New()))
	me := discord.User{ID: 2}
	guild := discord.Guild{ID: 1, Name: "guild", OwnerID: me.ID}
	category := discord.Channel{ID: 10, GuildID: guild.ID, Type: discord.GuildCategory, Name: "category"}
	forum := discord.Channel{ID: 11, GuildID: guild.ID, Type: discord.GuildForum, Name: "forum", ParentID: category.ID, LastMessageID: 100}
	old := discord.Channel{ID: 100, GuildID: guild.ID, Type: discord.GuildPublicThread, Name: "old", ParentID: forum.ID}
	if err := errors.Join(
		s.MyselfSet(me, false),
		s.GuildSet(&guild, false),
		s.MemberSet(guild.ID, &discord.Member{User: me}, false),
		s.RoleSet(guild.ID, &discord.Role{ID: discord.RoleID(guild.ID)}, false),
		s.ChannelSet(&category, false),
		s.ChannelSet(&forum, false),
		s.ChannelSet(&old, false),
	); err != nil {
		t.Fatal(err)
	}
	s.State.Call(&gateway.ReadyEvent{User: me, ReadStates: []gateway.ReadState{{ChannelID: forum.ID, LastMessageID: 100}}})

	cfg := config.Default()
	m := NewModel(&cfg, s)
	m.root.ClearChildren()
	m.createGuildNode(m.root, guild)
	m.selectNode(m.guildNodes[guild.ID])
	return m, s, forum
}

func TestModelForum(t *testing.T) {
	t.Run("selecting opens the posts", func(t *testing.T) {
		m, _, forum := newForumModel(t)
		node := m.channelNodes[forum.ID]
		if m.expandable(node) {
			t.Fatal("a forum expands rather than opening its posts")
		}
		task, ok := m.selectNode(node).(cmd.Task)
		if !ok {
			t.Fatal("selecting a forum does not open its posts")
		}
		if msg, ok := task().(OpenForumMsg); !ok || msg.Forum.ID != forum.ID {
			t.Fatalf("msg = %#v, want the forum's posts opened", msg)
		}
		if !node.Expanded() {
			t.Error("the posts under the forum are not shown")
		}
	})

	t.Run("a new post makes the forum unread", func(t *testing.T) {
		m, s, forum := newForumModel(t)
		node := m.channelNodes[forum.ID]
		if got := m.channelUnread(forum.ID); got != ningen.ChannelRead {
			t.Fatalf("channelUnread before the post = %v, want read", got)
		}

		post := discord.Channel{ID: 300, GuildID: forum.GuildID, Type: discord.GuildPublicThread, Name: "new", ParentID: forum.ID}
		// The state takes the post before the model hears of it.
		if err := s.ChannelSet(&post, false); err != nil {
			t.Fatal(err)
		}
		m, _ = m.Update(&gateway.ThreadCreateEvent{Channel: post, NewlyCreated: true})

		if got := m.channelUnread(forum.ID); got != ningen.ChannelUnread {
			t.Errorf("channelUnread after the post = %v, want unread", got)
		}
		if got, want := node.Spans()[0].Style, ui.UnreadStyle(ningen.ChannelUnread); got != want {
			t.Errorf("forum style = %v, want %v", got, want)
		}
		if children := node.Children(); len(children) != 2 || children[0] != m.channelNodes[post.ID] {
			t.Errorf("the new post is not first under the forum")
		}
	})

	t.Run("navigating shows a post not shown", func(t *testing.T) {
		m, s, forum := newForumModel(t)
		// A post that was neither active nor joined, so the state has it only once it is opened.
		post := discord.Channel{ID: 50, GuildID: forum.GuildID, Type: discord.GuildPublicThread, Name: "archived", ParentID: forum.ID}
		if err := s.ChannelSet(&post, false); err != nil {
			t.Fatal(err)
		}
		node := m.findNodeByChannelID(post.ID)
		if node == nil || !slices.Contains(m.channelNodes[forum.ID].Children(), node) {
			t.Fatal("the post is not shown under the forum")
		}
		// An old post is no newer than what the forum was read up to.
		if got := m.channelUnread(forum.ID); got != ningen.ChannelRead {
			t.Errorf("channelUnread = %v, want read", got)
		}
	})
}
