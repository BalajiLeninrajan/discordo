package messageslist

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
)

const (
	// maxReactors is how many of the users of a reaction are asked for, which is the most one request returns.
	maxReactors = 100
	// maxReactorNames is how many of them are named after the reaction.
	maxReactorNames = 10
)

// reactorsLoadedMsg names who reacted to a message, by emoji.
type reactorsLoadedMsg struct {
	channelID discord.ChannelID
	messageID discord.MessageID
	names     map[discord.APIEmoji][]string
}

func (reactorsLoadedMsg) isMsg() {}

// toggleReactors asks who reacted to the selected message, or hides them if they are shown.
func (m *Model) toggleReactors() tview.Cmd {
	message, ok := m.selectedMessage()
	if !ok || len(message.Reactions) == 0 {
		return nil
	}

	if _, shown := m.reactors[message.ID]; shown {
		delete(m.reactors, message.ID)
		m.items[m.cursor].view = nil
		return nil
	}
	return m.fetchReactors(*message)
}

// fetchReactors asks who reacted to message, a request for each of its reactions.
func (m Model) fetchReactors(message discord.Message) tview.Cmd {
	if m.channel == nil || len(message.Reactions) == 0 {
		return nil
	}

	guildID := m.channel.GuildID
	emojis := make([]discord.APIEmoji, len(message.Reactions))
	for index, reaction := range message.Reactions {
		emojis[index] = reaction.Emoji.APIString()
	}

	return cmd.Task(func() tview.Msg {
		names := make(map[discord.APIEmoji][]string, len(emojis))
		for _, emoji := range emojis {
			users, err := m.state.Reactions(message.ChannelID, message.ID, emoji, maxReactors)
			if err != nil {
				slog.Error("failed to get who reacted", "channel_id", message.ChannelID, "message_id", message.ID, "emoji", emoji, "err", err)
				continue
			}
			names[emoji] = m.reactorNames(guildID, users)
		}

		if len(names) == 0 {
			return nil
		}
		return reactorsLoadedMsg{channelID: message.ChannelID, messageID: message.ID, names: names}
	})
}

// reactorNames returns what users are called in the guild with guildID: their nickname there, or else their display name.
func (m Model) reactorNames(guildID discord.GuildID, users []discord.User) []string {
	names := make([]string, len(users))
	for index, user := range users {
		names[index] = user.DisplayOrUsername()
		if !guildID.IsValid() {
			continue
		}
		if member, _ := m.state.Cabinet.Member(guildID, user.ID); member != nil && member.Nick != "" {
			names[index] = member.Nick
		}
	}
	return names
}

// setReactors shows who reacted to a message of the selected channel, if it is still shown.
func (m *Model) setReactors(msg reactorsLoadedMsg) {
	index := m.indexOf(msg.channelID, msg.messageID)
	if index == -1 {
		return
	}

	if m.reactors == nil {
		m.reactors = make(map[discord.MessageID]map[discord.APIEmoji][]string)
	}
	m.reactors[msg.messageID] = msg.names
	m.items[index].view = nil
}

// reactorsLabel lists names, who reacted with a reaction that count users did, and how many more did after the first maxReactorNames.
func reactorsLabel(names []string, count int) string {
	shown := names[:min(len(names), maxReactorNames)]
	label := strings.Join(shown, ", ")
	if rest := count - len(shown); rest > 0 {
		if label != "" {
			label += " "
		}
		label += "+" + strconv.Itoa(rest)
	}
	return label
}
