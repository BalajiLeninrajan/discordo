package messageslist

import (
	"strconv"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
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
	names := map[discord.APIEmoji][]string{thumbsUp: {"ada"}}

	m.setReactors(reactorsLoadedMsg{channelID: 9, messageID: 2, names: names})
	if len(m.reactors) != 0 {
		t.Errorf("reactors = %v, want none for another channel", m.reactors)
	}

	m.setReactors(reactorsLoadedMsg{channelID: m.channel.ID, messageID: 2, names: names})
	if got := m.reactors[2][thumbsUp]; len(got) != 1 || got[0] != "ada" {
		t.Errorf("reactors = %v, want ada", m.reactors)
	}

	m.reset()
	if len(m.reactors) != 0 {
		t.Errorf("reactors = %v, want none after the channel changes", m.reactors)
	}
}
