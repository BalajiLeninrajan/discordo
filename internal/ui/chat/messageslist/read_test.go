package messageslist

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
)

func TestModelToggleRead(t *testing.T) {
	ml := newTestModel(t, testConfig(t))
	me, other := discord.User{ID: 1}, discord.User{ID: 2}
	ml.state.State.Call(&gateway.ReadyEvent{User: me, ReadStates: []gateway.ReadState{{ChannelID: ml.channel.ID, LastMessageID: 12}}})
	ml.setMessages([]discord.Message{
		{ID: 14, Author: other, Mentions: []discord.GuildUser{{User: me}, {User: me}}},
		{ID: 13, Author: me, Mentions: []discord.GuildUser{{User: me}}},
		{ID: 12, Author: other, Mentions: []discord.GuildUser{{User: me}}},
		{ID: 11, Author: other, Mentions: []discord.GuildUser{{User: other}}},
		{ID: 10, Author: other, Mentions: []discord.GuildUser{{User: me}}},
	})

	for id, want := range map[discord.MessageID]bool{10: false, 12: false, 13: true, 14: true} {
		if got := ml.unread(id); got != want {
			t.Errorf("unread(%d) = %v, want %v", id, got, want)
		}
	}

	// A message counts once however often it mentions the user, and not if the user wrote it.
	for id, want := range map[discord.MessageID]int{10: 3, 11: 2, 14: 1} {
		if got := ml.mentionsFrom(id); got != want {
			t.Errorf("mentionsFrom(%d) = %d, want %d", id, got, want)
		}
	}

	ml.cursor = -1
	if ml.toggleRead() != nil {
		t.Error("toggleRead without a selected message gives a command")
	}
}
