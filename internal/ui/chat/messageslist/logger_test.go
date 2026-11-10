package messageslist

import (
	"slices"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

// The test model is logged in as user 1 and shows channel 1, so user 2 is somebody else.
const (
	self  discord.UserID = 1
	other discord.UserID = 2
)

func testMessage(id discord.MessageID, author discord.UserID, content string) discord.Message {
	return discord.Message{ID: id, ChannelID: 1, Author: discord.User{ID: author}, Content: content}
}

// shownIDs returns the IDs of the messages shown, oldest first.
func shownIDs(m *Model) []discord.MessageID {
	var ids []discord.MessageID
	for _, item := range m.items {
		if item.separator == nil {
			ids = append(ids, item.message.ID)
		}
	}
	return ids
}

func TestModelDeleteMessageLogged(t *testing.T) {
	t.Run("keeps a message of somebody else", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		m.setMessages([]discord.Message{testMessage(11, other, "second"), testMessage(10, other, "first")})

		m.DeleteMessage(1, 10)

		if got, want := shownIDs(m), []discord.MessageID{10, 11}; !slices.Equal(got, want) {
			t.Fatalf("shown = %v, want %v", got, want)
		}
		if !m.log.isDeleted(10) || m.log.isDeleted(11) {
			t.Errorf("deleted = %v, want only 10", m.log.deletedIDs)
		}
	})

	t.Run("removes a message of the user", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		m.setMessages([]discord.Message{testMessage(11, other, "second"), testMessage(10, self, "first")})

		m.DeleteMessage(1, 10)

		if got, want := shownIDs(m), []discord.MessageID{11}; !slices.Equal(got, want) {
			t.Errorf("shown = %v, want %v", got, want)
		}
		if m.log.isDeleted(10) {
			t.Error("the user's own message was kept")
		}
	})

	t.Run("removes every message when disabled", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.MessageLogger.Enabled = false
		m := newTestModel(t, cfg)
		m.setMessages([]discord.Message{testMessage(10, other, "first")})

		m.DeleteMessage(1, 10)

		if got := shownIDs(m); len(got) != 0 {
			t.Errorf("shown = %v, want none", got)
		}
	})

	t.Run("keeps a message of a channel that is not shown", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		elsewhere := testMessage(20, other, "elsewhere")
		elsewhere.ChannelID = 2
		m.RememberPrior(elsewhere)

		m.DeleteMessage(2, 20)

		if !m.log.isDeleted(20) {
			t.Fatal("the message was not kept")
		}
		if len(m.log.priors) != 0 {
			t.Errorf("priors = %v, want none left", m.log.priors)
		}

		// It is shown once its channel is, among the messages still there.
		m.channel = &discord.Channel{ID: 2}
		m.reset()
		still := testMessage(21, other, "still here")
		still.ChannelID = 2
		older := testMessage(19, other, "older")
		older.ChannelID = 2
		m.setMessages([]discord.Message{still, older})
		if got, want := shownIDs(m), []discord.MessageID{19, 20, 21}; !slices.Equal(got, want) {
			t.Errorf("shown = %v, want %v", got, want)
		}
	})
}

func TestModelWithDeleted(t *testing.T) {
	m := newTestModel(t, testConfig(t))
	for _, id := range []discord.MessageID{5, 15, 25, 35} {
		m.log.addDeleted(testMessage(id, other, "deleted"))
	}
	items := func(ids ...discord.MessageID) []messageItem {
		items := make([]messageItem, len(ids))
		for index, id := range ids {
			items[index] = messageItem{message: testMessage(id, other, "")}
		}
		return items
	}
	ids := func(items []messageItem) []discord.MessageID {
		ids := make([]discord.MessageID, len(items))
		for index, item := range items {
			ids[index] = item.message.ID
		}
		return ids
	}

	for _, test := range []struct {
		name   string
		items  []messageItem
		before discord.MessageID
		want   []discord.MessageID
	}{
		// 5 is older than the oldest loaded, so it waits for its neighbours.
		{"newest messages", items(10, 20, 30), 0, []discord.MessageID{10, 15, 20, 25, 30, 35}},
		{"older messages", items(10, 20), 30, []discord.MessageID{10, 15, 20, 25}},
		{"no messages", nil, 0, []discord.MessageID{5, 15, 25, 35}},
		{"already among them", items(10, 15, 20), 25, []discord.MessageID{10, 15, 20}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ids(m.withDeleted(test.items, test.before)); !slices.Equal(got, test.want) {
				t.Errorf("withDeleted = %v, want %v", got, test.want)
			}
		})
	}
}

func TestModelUpdateMessageLogged(t *testing.T) {
	edited := func(message discord.Message, content string, at time.Time) discord.Message {
		message.Content, message.EditedTimestamp = content, discord.NewTimestamp(at)
		return message
	}
	now := time.Now()

	t.Run("keeps what the message said", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		original := testMessage(10, other, "one")
		m.setMessages([]discord.Message{original})

		second := edited(original, "two", now)
		m.UpdateMessage(second)
		m.UpdateMessage(edited(second, "three", now.Add(time.Minute)))

		history := m.log.history(10)
		if len(history) != 2 || history[0].content != "one" || history[1].content != "two" {
			t.Errorf("history = %v, want one then two", history)
		}
	})

	t.Run("ignores an update that is not an edit", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		original := testMessage(10, other, "one")
		m.setMessages([]discord.Message{original})

		// An embed that loads updates the message without editing it.
		m.UpdateMessage(original)

		if history := m.log.history(10); len(history) != 0 {
			t.Errorf("history = %v, want none", history)
		}
	})

	t.Run("ignores an edit of the user", func(t *testing.T) {
		m := newTestModel(t, testConfig(t))
		original := testMessage(10, self, "one")
		m.setMessages([]discord.Message{original})

		m.UpdateMessage(edited(original, "two", now))

		if history := m.log.history(10); len(history) != 0 {
			t.Errorf("history = %v, want none", history)
		}
	})
}

func TestModelForgetSelected(t *testing.T) {
	m := newTestModel(t, testConfig(t))
	m.setMessages([]discord.Message{testMessage(11, other, "second"), testMessage(10, other, "first")})
	m.DeleteMessage(1, 10)

	m.selectBottom()
	if m.forgetSelected() {
		t.Fatal("forgot a message that was not deleted")
	}

	m.selectTop()
	if !m.forgetSelected() {
		t.Fatal("did not forget the deleted message")
	}
	if got, want := shownIDs(m), []discord.MessageID{11}; !slices.Equal(got, want) {
		t.Errorf("shown = %v, want %v", got, want)
	}
	if m.log.isDeleted(10) || len(m.log.deleted[1]) != 0 {
		t.Errorf("log still has the message: %v", m.log.deleted)
	}
}

func TestMessageLogAddDeleted(t *testing.T) {
	log := newMessageLog()
	for id := discord.MessageID(maxLoggedDeleted + 2); id >= 1; id-- {
		log.addDeleted(testMessage(id, other, ""))
	}
	log.addDeleted(testMessage(5, other, ""))

	logged := log.deleted[1]
	if len(logged) != maxLoggedDeleted {
		t.Fatalf("kept %d messages, want %d", len(logged), maxLoggedDeleted)
	}
	if !slices.IsSortedFunc(logged, func(a, b discord.Message) int { return int(a.ID) - int(b.ID) }) {
		t.Error("the messages are not in order")
	}
	if len(log.deletedIDs) != maxLoggedDeleted {
		t.Errorf("deletedIDs has %d, want %d", len(log.deletedIDs), maxLoggedDeleted)
	}
}
