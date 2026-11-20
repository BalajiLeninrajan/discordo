package messageslist

import (
	"log/slog"
	"strconv"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/markdown"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
)

// OpenThreadMsg asks to open Thread, which the state may not have.
type OpenThreadMsg struct{ Thread discord.Channel }

// threadOf returns the thread that message started or announces, as the state has it if it does, or nil.
func (m Model) threadOf(message discord.Message) *discord.Channel {
	var thread *discord.Channel
	switch {
	case message.Thread != nil:
		thread = message.Thread
	case message.Type == discord.ThreadCreatedMessage && message.Reference != nil && message.Reference.ChannelID.IsValid():
		// Only the ID of an announced thread is known, and its name is the content.
		thread = &discord.Channel{ID: message.Reference.ChannelID, GuildID: message.GuildID, Name: message.Content}
	default:
		return nil
	}

	if cached, err := m.state.Cabinet.Channel(thread.ID); err == nil {
		return cached
	}
	return thread
}

// openThread asks to open the thread of the selected message, fetching it if only its ID is known.
func (m Model) openThread() tview.Cmd {
	message, ok := m.selectedMessage()
	if !ok {
		return nil
	}
	known := m.threadOf(*message)
	if known == nil {
		return nil
	}

	thread, state := *known, m.state
	return cmd.Task(func() tview.Msg {
		if !thread.Type.IsThread() {
			fetched, err := state.Channel(thread.ID)
			if err != nil {
				slog.Error("failed to fetch thread", "channel_id", thread.ID, "err", err)
				return nil
			}
			thread = *fetched
		}
		return OpenThreadMsg{Thread: thread}
	})
}

// drawThread writes the thread that message started: its name and how many messages it has.
func (m Model) drawThread(builder *markdown.Builder, thread discord.Channel, baseStyle tview.Style) {
	dimStyle := baseStyle.Dim(true)
	builder.Write(ui.ChannelToString(thread, m.cfg.Icons, m.state), dimStyle.Bold(true))
	if count := thread.MessageCount; count > 0 {
		label := strconv.Itoa(count) + " messages"
		if count == 1 {
			label = "1 message"
		}
		builder.Write(" ", baseStyle)
		builder.Write(label, dimStyle)
	}
}

// newItem returns the item that shows message.
func newItem(message discord.Message) messageItem {
	return messageItem{message: displayed(message)}
}

// displayed returns the message to show for message. The first message of a thread started from a message is empty
// and refers to that message, which it is shown as.
func displayed(message discord.Message) discord.Message {
	ref := message.ReferencedMessage
	if message.Type != discord.ThreadStarterMessage || ref == nil {
		return message
	}

	shown := *ref
	// It stays the first message of the thread, which logs and actions refer to, and which cannot be edited.
	shown.ID, shown.ChannelID, shown.GuildID, shown.Type = message.ID, message.ChannelID, message.GuildID, message.Type
	shown.Reactions = message.Reactions
	// It is shown in the thread it started, and what it replies to is not sent with it.
	shown.Thread, shown.ReferencedMessage = nil, nil
	if shown.Reference != nil && shown.Reference.Type != discord.MessageReferenceTypeForward {
		shown.Reference = nil
	}
	return shown
}

// SetThread shows thread under the message it was started from, if that is shown. Such a thread has the ID of the
// message, and a thread started without one has none to show it under.
func (m *Model) SetThread(thread discord.Channel) {
	index := m.indexOf(thread.ParentID, discord.MessageID(thread.ID))
	if index == -1 {
		return
	}
	item := &m.items[index]
	item.message.Thread = &thread
	item.view = nil
}
