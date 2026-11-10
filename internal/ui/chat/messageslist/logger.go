package messageslist

import (
	"cmp"
	"slices"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/discordo/internal/ui"
)

// maxLoggedDeleted is how many deleted messages are kept per channel, the oldest making way for the newest.
const maxLoggedDeleted = 500

const deletedIndicator = "(deleted)"

// PriorEvent is a message as the state held it before the gateway event that deletes or edits it. The state drops or
// replaces the message before that event is handled, so its copy is sent ahead.
type PriorEvent struct {
	Message discord.Message
}

func (PriorEvent) Op() ws.OpCode           { return -1 }
func (PriorEvent) EventType() ws.EventType { return "__messageslist.PriorEvent" }

// edit is what a message said until it was edited at replaced.
type edit struct {
	content  string
	replaced discord.Timestamp
}

// messageLog keeps the messages that were deleted and what messages said before their edits, of every channel.
type messageLog struct {
	// priors are the messages sent ahead of the events that delete or edit them, until those events are handled.
	priors map[discord.MessageID]discord.Message
	// deleted are the deleted messages of each channel, oldest first.
	deleted    map[discord.ChannelID][]discord.Message
	deletedIDs map[discord.MessageID]struct{}
	edits      map[discord.MessageID][]edit
}

func newMessageLog() *messageLog {
	return &messageLog{
		priors:     make(map[discord.MessageID]discord.Message),
		deleted:    make(map[discord.ChannelID][]discord.Message),
		deletedIDs: make(map[discord.MessageID]struct{}),
		edits:      make(map[discord.MessageID][]edit),
	}
}

// isDeleted reports whether the message with id was deleted and is kept.
func (l *messageLog) isDeleted(id discord.MessageID) bool {
	if l == nil {
		return false
	}
	_, ok := l.deletedIDs[id]
	return ok
}

// history returns what the message with id said before each of its edits, oldest first.
func (l *messageLog) history(id discord.MessageID) []edit {
	if l == nil {
		return nil
	}
	return l.edits[id]
}

// addDeleted keeps message, which was deleted, in the order of its channel.
func (l *messageLog) addDeleted(message discord.Message) {
	if l.isDeleted(message.ID) {
		return
	}

	logged := l.deleted[message.ChannelID]
	index, _ := slices.BinarySearchFunc(logged, message.ID, func(logged discord.Message, id discord.MessageID) int {
		return cmp.Compare(logged.ID, id)
	})
	logged = slices.Insert(logged, index, message)
	l.deletedIDs[message.ID] = struct{}{}
	// The oldest makes way, which is the message itself if it is older than all those kept.
	if len(logged) > maxLoggedDeleted {
		delete(l.deletedIDs, logged[0].ID)
		delete(l.edits, logged[0].ID)
		logged = logged[1:]
	}
	l.deleted[message.ChannelID] = logged
}

// forget drops the deleted message with id of the channel with channelID.
func (l *messageLog) forget(channelID discord.ChannelID, id discord.MessageID) {
	l.deleted[channelID] = slices.DeleteFunc(l.deleted[channelID], func(message discord.Message) bool { return message.ID == id })
	delete(l.deletedIDs, id)
	delete(l.edits, id)
}

// addEdit keeps what prior said if message, which replaces it, is an edit that says something else.
func (l *messageLog) addEdit(prior, message discord.Message) {
	edited := message.EditedTimestamp
	if !edited.IsValid() || edited.Time().Equal(prior.EditedTimestamp.Time()) {
		return
	}
	if prior.Content == "" || prior.Content == message.Content {
		return
	}
	l.edits[message.ID] = append(l.edits[message.ID], edit{content: prior.Content, replaced: edited})
}

// logs reports whether what happens to message is kept: the logger is enabled, and the message is not the user's own.
func (m Model) logs(message discord.Message) bool {
	return m.log != nil && m.cfg.MessageLogger.Enabled && !ui.IsMe(m.state, message.Author.ID)
}

// RememberPrior keeps message, as it was before the event that deletes or edits it, until that event is handled.
func (m *Model) RememberPrior(message discord.Message) {
	if m.logs(message) {
		m.log.priors[message.ID] = message
	}
}

// takePrior returns the message with id as it was before the event being handled: the copy sent ahead of the event, or else the message shown.
func (m *Model) takePrior(channelID discord.ChannelID, id discord.MessageID) (discord.Message, bool) {
	if m.log != nil {
		if prior, ok := m.log.priors[id]; ok {
			delete(m.log.priors, id)
			prior.ChannelID = channelID
			return prior, true
		}
	}

	if index := m.indexOf(channelID, id); index != -1 {
		return m.items[index].message, true
	}
	return discord.Message{}, false
}

// logDeleted keeps the message with id, which was deleted, and reports whether it did. A message kept stays shown, as deleted.
func (m *Model) logDeleted(channelID discord.ChannelID, id discord.MessageID) bool {
	if m.log.isDeleted(id) {
		return true
	}

	prior, ok := m.takePrior(channelID, id)
	if !ok || !m.logs(prior) {
		return false
	}

	m.log.addDeleted(prior)
	if index := m.indexOf(channelID, id); index != -1 {
		m.items[index].view = nil
	}
	return true
}

// logEdit keeps what the message that message replaces said, if message is an edit of it.
func (m *Model) logEdit(message discord.Message) {
	prior, ok := m.takePrior(message.ChannelID, message.ID)
	if ok && m.logs(prior) {
		m.log.addEdit(prior, message)
	}
}

// forgetSelected removes the selected message if it is a deleted one that was kept, and reports whether it was.
func (m *Model) forgetSelected() bool {
	message, ok := m.selectedMessage()
	if !ok || !m.log.isDeleted(message.ID) {
		return false
	}

	m.log.forget(message.ChannelID, message.ID)
	m.deleteMessage(m.cursor)
	return true
}

// withDeleted returns items, the messages of the selected channel oldest first, with its deleted messages that were kept among them.
// Only those newer than the oldest of items are added, and older than before if it is valid, as the others belong with messages that are not loaded.
func (m Model) withDeleted(items []messageItem, before discord.MessageID) []messageItem {
	if m.log == nil || m.channel == nil {
		return items
	}
	logged := m.log.deleted[m.channel.ID]
	if len(logged) == 0 {
		return items
	}

	var after discord.MessageID
	if len(items) > 0 {
		after = items[0].message.ID
	}

	merged := make([]messageItem, 0, len(items)+len(logged))
	next := 0
	for _, message := range logged {
		if message.ID <= after || (before.IsValid() && message.ID >= before) {
			continue
		}

		for next < len(items) && items[next].message.ID < message.ID {
			merged = append(merged, items[next])
			next++
		}
		if next < len(items) && items[next].message.ID == message.ID {
			continue
		}
		merged = append(merged, messageItem{message: message})
	}
	return append(merged, items[next:]...)
}
