package messageslist

import (
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
)

func renderedText(ml *Model, message discord.Message) string {
	var got strings.Builder
	for _, span := range ml.renderMessage(message, nil, tview.Style{}, false) {
		got.WriteString(span.Text)
	}
	return got.String()
}

func TestModelThreadCreatedMessage(t *testing.T) {
	cfg := testConfig(t)
	cfg.Timestamps.Enabled = false
	ml := newTestModel(t, cfg)
	message := discord.Message{
		Type:      discord.ThreadCreatedMessage,
		Author:    discord.User{Username: "a"},
		Content:   "old name",
		Reference: &discord.MessageReference{ChannelID: 5},
	}

	t.Run("names the thread from its content", func(t *testing.T) {
		if got, want := renderedText(ml, message), "a\nstarted a thread: old name"; got != want {
			t.Fatalf("got = %q, want = %q", got, want)
		}
	})

	thread := discord.Channel{ID: 5, GuildID: 9, ParentID: 1, Type: discord.GuildPublicThread, Name: "new name"}
	if err := ml.state.ChannelSet(&thread, false); err != nil {
		t.Fatal(err)
	}
	t.Run("names the thread as the state has it", func(t *testing.T) {
		if got, want := renderedText(ml, message), "a\nstarted a thread: new name"; got != want {
			t.Fatalf("got = %q, want = %q", got, want)
		}
	})
	t.Run("opens the thread", func(t *testing.T) {
		ml.setMessages([]discord.Message{message})
		ml.setCursor(ml.messageIndex(-1, 1))
		msg, ok := ml.openThread().(cmd.Task)().(OpenThreadMsg)
		if !ok || msg.Thread.ID != thread.ID {
			t.Fatalf("got = %#v, want the thread with ID %d", msg, thread.ID)
		}
	})
}

func TestDisplayed(t *testing.T) {
	original := discord.Message{
		ID:        5,
		ChannelID: 1,
		Type:      discord.InlinedReplyMessage,
		Author:    discord.User{ID: 2, Username: "a"},
		Content:   "hi",
		Reference: &discord.MessageReference{MessageID: 4},
		Thread:    &discord.Channel{ID: 5},
	}
	starter := discord.Message{ID: 7, ChannelID: 5, Type: discord.ThreadStarterMessage, ReferencedMessage: &original}

	shown := displayed(starter)
	if shown.ID != starter.ID || shown.ChannelID != starter.ChannelID || shown.Type != discord.ThreadStarterMessage {
		t.Fatalf("shown as message %d in channel %d of type %d, want the starter's", shown.ID, shown.ChannelID, shown.Type)
	}
	if shown.Author.ID != original.Author.ID || shown.Content != original.Content {
		t.Fatal("the original message is not shown")
	}
	if shown.Thread != nil || shown.Reference != nil {
		t.Fatal("the thread or what the original replies to is shown")
	}

	cfg := testConfig(t)
	cfg.Timestamps.Enabled = false
	if got, want := renderedText(newTestModel(t, cfg), shown), "a\nhi"; got != want {
		t.Fatalf("got = %q, want = %q", got, want)
	}
}

func TestModelSetThread(t *testing.T) {
	ml := newTestModel(t, testConfig(t))
	ml.setMessages([]discord.Message{{ID: 6, ChannelID: 1}, {ID: 5, ChannelID: 1}})

	ml.SetThread(discord.Channel{ID: 5, ParentID: 1, Name: "thread"})
	index := ml.indexOf(1, 5)
	if thread := ml.items[index].message.Thread; thread == nil || thread.Name != "thread" {
		t.Fatal("the thread is not shown under the message it was started from")
	}
	if ml.items[ml.indexOf(1, 6)].message.Thread != nil {
		t.Fatal("the thread is shown under another message")
	}
}
