package messageslist

import (
	"slices"
	"strconv"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestReactorsLabel(t *testing.T) {
	many := make([]string, maxReactorNames+5)
	for index := range many {
		many[index] = strconv.Itoa(index)
	}

	for _, test := range []struct {
		name  string
		names []string
		count int
		want  string
	}{
		{"one", []string{"ada"}, 1, "ada"},
		{"several", []string{"ada", "bob"}, 2, "ada, bob"},
		{"more reacted than were fetched", []string{"ada"}, 3, "ada +2"},
		{"more than are named", many, len(many), "0, 1, 2, 3, 4, 5, 6, 7, 8, 9 +5"},
		{"none fetched", nil, 2, "+2"},
		{"count behind the names", []string{"ada", "bob"}, 1, "ada, bob"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := reactorsLabel(test.names, test.count); got != test.want {
				t.Errorf("reactorsLabel = %q, want %q", got, test.want)
			}
		})
	}
}

func TestModelSetReactors(t *testing.T) {
	m := newTestModel(t, testConfig(t))
	m.setMessages([]discord.Message{{ID: 2, ChannelID: m.channel.ID}})
	thumbsUp := discord.Emoji{Name: "\U0001F44D"}.APIString()
	users := map[discord.APIEmoji][]discord.User{thumbsUp: {{ID: 5, Username: "ada"}}}

	m.setReactors(reactorsLoadedMsg{channelID: 9, messageID: 2, users: users})
	if len(m.reactors) != 0 {
		t.Errorf("reactors = %v, want none for another channel", m.reactors)
	}

	m.setReactors(reactorsLoadedMsg{channelID: m.channel.ID, messageID: 2, users: users})
	if got := m.reactorNamesOf(2)[thumbsUp]; len(got) != 1 || got[0] != "ada" {
		t.Errorf("reactors = %v, want ada", m.reactorNamesOf(2))
	}

	m.reset()
	if len(m.reactors) != 0 {
		t.Errorf("reactors = %v, want none after the channel changes", m.reactors)
	}
}

// reacted returns a message of channel 1 with a reaction of each of emojis.
func reacted(id discord.MessageID, emojis ...string) discord.Message {
	message := discord.Message{ID: id, ChannelID: 1}
	for _, emoji := range emojis {
		message.Reactions = append(message.Reactions, discord.Reaction{Count: 1, Emoji: discord.Emoji{Name: emoji}})
	}
	return message
}

func TestModelQueueReactors(t *testing.T) {
	t.Run("asks about the messages in view, newest first, one at a time", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		m.setMessages([]discord.Message{reacted(4, "a"), {ID: 3, ChannelID: 1}, reacted(2, "b")})
		if m.queueReactors() == nil || !m.reactorsQueue.busy {
			t.Fatal("who reacted is not asked for")
		}
		if want := []discord.MessageID{2}; !slices.Equal(m.reactorsQueue.pending, want) {
			t.Fatalf("pending = %v, want %v after asking about the newest", m.reactorsQueue.pending, want)
		}
		if m.queueReactors() != nil {
			t.Fatal("asked again while a request is on its way")
		}

		next, command := m.Update(reactorsLoadedMsg{channelID: 1, messageID: 4, queued: true})
		if command == nil || len(next.reactorsQueue.pending) != 0 {
			t.Fatal("the next message is not asked about once the first is in")
		}
	})

	t.Run("skips messages with many kinds of reaction", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		emojis := make([]string, maxQueuedReactions+1)
		for i := range emojis {
			emojis[i] = string(rune('a' + i))
		}
		m.setMessages([]discord.Message{reacted(2, emojis...)})
		if m.queueReactors() != nil {
			t.Fatal("asked about a message with too many kinds of reaction")
		}
	})

	t.Run("asks once per message", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		m.setMessages([]discord.Message{reacted(2, "a")})
		m.queueReactors()
		m.reactorsQueue.busy = false
		if m.queueReactors() != nil {
			t.Fatal("asked about the same message twice")
		}
	})

	t.Run("off", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.UI.MessagesList.Reactions.ShowReactors = false
		m := newTestModel(t, cfg)
		m.setMessages([]discord.Message{reacted(2, "a")})
		if m.queueReactors() != nil {
			t.Fatal("who reacted is asked for while off")
		}
	})

	t.Run("only what is in view", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		messages := []discord.Message{}
		for id := discord.MessageID(200); id > 100; id-- {
			messages = append(messages, reacted(id, "a"))
		}
		m.setMessages(messages)
		visible := m.visibleMessages()
		if len(visible) == 0 || len(visible) >= len(messages) {
			t.Fatalf("%d of %d messages are in view", len(visible), len(messages))
		}
		if newest := m.items[visible[0]].message.ID; newest != 200 {
			t.Fatalf("first in view = %d, want the newest", newest)
		}
	})
}

func TestModelReactionEvents(t *testing.T) {
	thumbsUp := discord.Emoji{Name: "\U0001F44D"}
	m := newTestModel(t, testConfig(t))
	m.setMessages([]discord.Message{reacted(2, thumbsUp.Name)})
	m.setReactors(reactorsLoadedMsg{channelID: 1, messageID: 2, users: map[discord.APIEmoji][]discord.User{thumbsUp.APIString(): {{ID: 5, Username: "ada"}}}})

	m.OnReactionAdd(&gateway.MessageReactionAddEvent{ChannelID: 1, MessageID: 2, UserID: 6, Emoji: thumbsUp, Member: &discord.Member{User: discord.User{ID: 6, Username: "bea"}}})
	if got := m.reactorNamesOf(2)[thumbsUp.APIString()]; !slices.Equal(got, []string{"ada", "bea"}) {
		t.Fatalf("after an add, reactors = %v", got)
	}

	m.OnReactionRemove(&gateway.MessageReactionRemoveEvent{ChannelID: 1, MessageID: 2, UserID: 5, Emoji: thumbsUp})
	if got := m.reactorNamesOf(2)[thumbsUp.APIString()]; !slices.Equal(got, []string{"bea"}) {
		t.Fatalf("after a remove, reactors = %v", got)
	}

	m.OnReactionsRemoved(1, 2, "")
	if got := m.reactorNamesOf(2); len(got) != 0 {
		t.Fatalf("after removing all, reactors = %v", got)
	}
}
