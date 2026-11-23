package messageslist

import (
	"log/slog"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
)

// toggleRead marks the channel read up to the selected message if it is unread, or else unread from it on.
func (m Model) toggleRead() tview.Cmd {
	message, ok := m.selectedMessage()
	if !ok || m.channel == nil {
		return nil
	}

	channelID, messageID, state := m.channel.ID, message.ID, m.state
	if m.unread(messageID) {
		return cmd.Task(func() tview.Msg {
			state.ReadState.MarkRead(channelID, messageID)
			return nil
		})
	}

	mentions := m.mentionsFrom(messageID)
	return cmd.Task(func() tview.Msg {
		if err := ui.MarkUnread(state, channelID, messageID, mentions); err != nil {
			slog.Error("failed to mark message unread", "err", err, "channel_id", channelID, "message_id", messageID)
		}
		return nil
	})
}

// unread reports whether the message with id in the channel is after the newest one read.
func (m Model) unread(id discord.MessageID) bool {
	state := m.state.ReadState.ReadState(m.channel.ID)
	return state != nil && id > state.LastMessageID
}

// mentionsFrom counts the loaded messages from the one with id on that others wrote mentioning the user.
func (m Model) mentionsFrom(id discord.MessageID) int {
	me, err := m.state.Cabinet.Me()
	if err != nil {
		return 0
	}

	count := 0
	for _, item := range m.items {
		message := item.message
		if item.separator != nil || message.ID < id || message.Author.ID == me.ID || m.log.isDeleted(message.ID) {
			continue
		}
		for _, user := range message.Mentions {
			if user.ID == me.ID {
				count++
				break
			}
		}
	}
	return count
}
