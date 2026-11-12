package messageslist

import (
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
	"github.com/ayn2op/tview/layout"
)

const (
	// maxReactors is how many of the users of a reaction are asked for, which is the most one request returns.
	maxReactors = 100
	// maxReactorNames is how many of them are named after the reaction.
	maxReactorNames = 10
	// maxQueuedReactions is the most kinds of reaction a message can have to be asked about as it comes into view, as each kind takes a request.
	maxQueuedReactions = 10
	// queuePause is how long the queue waits after each request, to keep clear of Discord's rate limits.
	queuePause = 250 * time.Millisecond
	// guessedMessages is how many messages are taken to be in view before the list reports its size, which it does once it is scrolled.
	guessedMessages = 15
)

// reactorsLoadedMsg says who reacted to a message, by emoji. queued reports whether the queue asked, which then goes on to the next message.
type reactorsLoadedMsg struct {
	channelID discord.ChannelID
	messageID discord.MessageID
	users     map[discord.APIEmoji][]discord.User
	queued    bool
}

func (reactorsLoadedMsg) isMsg() {}

// reactorsQueue holds the messages to ask who reacted to as they come into view, one at a time. Copies of the model share it.
type reactorsQueue struct {
	// asked are the messages queued once, which are not queued again.
	asked map[discord.MessageID]struct{}
	// pending are the messages to ask about, the next first.
	pending []discord.MessageID
	// busy reports whether a request of the queue is on its way.
	busy bool
}

func newReactorsQueue() *reactorsQueue {
	return &reactorsQueue{asked: make(map[discord.MessageID]struct{})}
}

// reset forgets the messages queued, as the channel changed. A request on its way still ends the busy state when it is in.
func (q *reactorsQueue) reset() {
	clear(q.asked)
	q.pending = nil
}

// showsReactors reports whether who reacted is asked for as messages come into view, rather than only with the keybind.
func (m Model) showsReactors() bool {
	cfg := m.cfg.UI.MessagesList.Reactions
	return cfg.Enabled && cfg.ShowReactors
}

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
	return m.fetchReactors(*message, false)
}

// queueReactors queues the messages in view, newest first, ahead of those queued before, as Vencord's WhoReacted does, and asks about the next if no request is on its way.
func (m Model) queueReactors() tview.Cmd {
	if !m.showsReactors() || m.channel == nil {
		return nil
	}

	var fresh []discord.MessageID
	for _, index := range m.visibleMessages() {
		message := m.items[index].message
		if _, asked := m.reactorsQueue.asked[message.ID]; asked || len(message.Reactions) == 0 || len(message.Reactions) > maxQueuedReactions || m.blocked(message.Author) {
			continue
		}
		m.reactorsQueue.asked[message.ID] = struct{}{}
		fresh = append(fresh, message.ID)
	}
	m.reactorsQueue.pending = append(fresh, m.reactorsQueue.pending...)
	return m.nextReactors()
}

// nextReactors asks about the next message queued that is still shown, unless a request of the queue is on its way.
func (m Model) nextReactors() tview.Cmd {
	q := m.reactorsQueue
	for !q.busy && len(q.pending) != 0 && m.channel != nil {
		id := q.pending[0]
		q.pending = q.pending[1:]
		if index := m.indexOf(m.channel.ID, id); index != -1 {
			q.busy = true
			return m.fetchReactors(m.items[index].message, true)
		}
	}
	return nil
}

// visibleMessages returns the indexes of the messages in view, and of those around the selection, which the list keeps in the middle, newest first.
// It lays out the items from the newest only as far up as those, at the width the list was drawn at, so that their wrapped text stays cached for drawing.
func (m Model) visibleMessages() []int {
	view := m.scrollState.Viewport()
	width, height := view.Bounds().Width, view.Bounds().Height
	if width <= 0 || height <= 0 {
		return m.guessVisibleMessages()
	}

	// Rows are counted up from the bottom, which the list is anchored at.
	limits := layout.Limits{Max: layout.Size{Width: width}, Infinite: layout.Axes{Height: true}}
	scrolled := view.AbsoluteOffset().Y
	windows := [][2]int{{scrolled, scrolled + height}}
	upTo := scrolled + height
	bottoms, tops := make([]int, len(m.items)), make([]int, len(m.items))
	last, below := len(m.items), 0
	for index := len(m.items) - 1; index >= 0; index-- {
		bottoms[index] = below
		below += m.buildItem(index).Layout(limits).Height
		tops[index] = below
		last = index
		if index == m.cursor {
			middle := (bottoms[index] + tops[index]) / 2
			windows = append(windows, [2]int{middle - height/2, middle + height/2 + 1})
			upTo = max(upTo, middle+height/2+1)
		}
		if below >= upTo && index <= max(m.cursor, 0) {
			break
		}
		// The spacing above an item that starts a group.
		if index > 0 && !m.items[index].grouped {
			below += int(m.cfg.UI.MessagesList.Spacing)
		}
	}

	var visible []int
	for index := len(m.items) - 1; index >= last; index-- {
		if m.items[index].separator != nil {
			continue
		}
		if slices.ContainsFunc(windows, func(w [2]int) bool { return bottoms[index] < w[1] && tops[index] > w[0] }) {
			visible = append(visible, index)
		}
	}
	return visible
}

// guessVisibleMessages returns the newest messages, and those around the selection, by count, before the list reports its size by scrolling.
func (m Model) guessVisibleMessages() []int {
	var visible []int
	newest := 0
	for index := len(m.items) - 1; index >= 0; index-- {
		if m.items[index].separator != nil {
			continue
		}
		near := m.cursor >= 0 && index >= m.cursor-guessedMessages/2 && index <= m.cursor+guessedMessages/2
		if newest < guessedMessages || near {
			visible = append(visible, index)
		}
		newest++
	}
	return visible
}

// fetchReactors asks who reacted to message, a request for each of its reactions. A queued request pauses after it, and always answers, so that the queue goes on.
func (m Model) fetchReactors(message discord.Message, queued bool) tview.Cmd {
	if m.channel == nil || len(message.Reactions) == 0 {
		return nil
	}

	emojis := make([]discord.APIEmoji, len(message.Reactions))
	for index, reaction := range message.Reactions {
		emojis[index] = reaction.Emoji.APIString()
	}

	return cmd.Task(func() tview.Msg {
		users := make(map[discord.APIEmoji][]discord.User, len(emojis))
		for _, emoji := range emojis {
			reactors, err := m.state.Reactions(message.ChannelID, message.ID, emoji, maxReactors)
			if err != nil {
				slog.Error("failed to get who reacted", "channel_id", message.ChannelID, "message_id", message.ID, "emoji", emoji, "err", err)
				continue
			}
			users[emoji] = reactors
		}

		if queued {
			time.Sleep(queuePause)
		} else if len(users) == 0 {
			return nil
		}
		return reactorsLoadedMsg{channelID: message.ChannelID, messageID: message.ID, users: users, queued: queued}
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

// reactorNamesOf names who reacted to the message with id, by emoji, or returns nil if they are not shown.
func (m Model) reactorNamesOf(id discord.MessageID) map[discord.APIEmoji][]string {
	reactors, ok := m.reactors[id]
	if !ok {
		return nil
	}

	var guildID discord.GuildID
	if m.channel != nil {
		guildID = m.channel.GuildID
	}
	names := make(map[discord.APIEmoji][]string, len(reactors))
	for emoji, users := range reactors {
		names[emoji] = m.reactorNames(guildID, users)
	}
	return names
}

// setReactors shows who reacted to a message of the selected channel, if it is still shown.
func (m *Model) setReactors(msg reactorsLoadedMsg) {
	index := m.indexOf(msg.channelID, msg.messageID)
	if index == -1 || len(msg.users) == 0 {
		return
	}

	if m.reactors == nil {
		m.reactors = make(map[discord.MessageID]map[discord.APIEmoji][]discord.User)
	}
	m.reactors[msg.messageID] = msg.users
	m.items[index].view = nil
}

// editReactors changes who reacted to the message with id in channelID with edit, if they are shown.
func (m *Model) editReactors(channelID discord.ChannelID, id discord.MessageID, edit func(map[discord.APIEmoji][]discord.User)) {
	reactors, ok := m.reactors[id]
	if !ok || m.indexOf(channelID, id) == -1 {
		return
	}
	edit(reactors)
}

// reactor returns the user with id among the members of the channel's guild, the user logged in or the channel's recipients, or false if they are not known.
func (m Model) reactor(id discord.UserID) (discord.User, bool) {
	if m.channel == nil {
		return discord.User{}, false
	}
	if m.channel.GuildID.IsValid() {
		if member, err := m.state.Cabinet.Member(m.channel.GuildID, id); err == nil {
			return member.User, true
		}
	}
	if me, err := m.state.Cabinet.Me(); err == nil && me.ID == id {
		return *me, true
	}
	index := slices.IndexFunc(m.channel.DMRecipients, func(user discord.User) bool { return user.ID == id })
	if index == -1 {
		return discord.User{}, false
	}
	return m.channel.DMRecipients[index], true
}

// addReactor adds user to who reacted with emoji to the message with id, if they are shown.
func (m *Model) addReactor(channelID discord.ChannelID, id discord.MessageID, emoji discord.APIEmoji, user discord.User) {
	m.editReactors(channelID, id, func(reactors map[discord.APIEmoji][]discord.User) {
		users := reactors[emoji]
		if !slices.ContainsFunc(users, func(u discord.User) bool { return u.ID == user.ID }) {
			reactors[emoji] = append(users, user)
		}
	})
}

// OnReactionAdd shows the reaction an event added, with who added it if who reacted is shown. Who reacted is asked for again only when that user is not known.
func (m *Model) OnReactionAdd(event *gateway.MessageReactionAddEvent) tview.Cmd {
	var user discord.User
	var ok bool
	if event.Member != nil {
		user, ok = event.Member.User, true
	} else {
		user, ok = m.reactor(event.UserID)
	}
	if !ok {
		return cmd.Batch{m.RefreshMessage(event.ChannelID, event.MessageID), m.refetchReactors(event.ChannelID, event.MessageID)}
	}
	m.addReactor(event.ChannelID, event.MessageID, event.Emoji.APIString(), user)
	return m.RefreshMessage(event.ChannelID, event.MessageID)
}

// OnReactionAddMany shows the reactions an event added, with who added them if who reacted is shown.
func (m *Model) OnReactionAddMany(event *gateway.MessageReactionAddManyEvent) tview.Cmd {
	for _, reaction := range event.Reactions {
		for _, id := range reaction.Users {
			user, ok := m.reactor(id)
			if !ok {
				return cmd.Batch{m.RefreshMessage(event.ChannelID, event.MessageID), m.refetchReactors(event.ChannelID, event.MessageID)}
			}
			m.addReactor(event.ChannelID, event.MessageID, reaction.Emoji.APIString(), user)
		}
	}
	return m.RefreshMessage(event.ChannelID, event.MessageID)
}

// OnReactionRemove shows the reaction an event removed, and drops who removed it from who reacted.
func (m *Model) OnReactionRemove(event *gateway.MessageReactionRemoveEvent) tview.Cmd {
	emoji := event.Emoji.APIString()
	m.editReactors(event.ChannelID, event.MessageID, func(reactors map[discord.APIEmoji][]discord.User) {
		reactors[emoji] = slices.DeleteFunc(reactors[emoji], func(user discord.User) bool { return user.ID == event.UserID })
		if len(reactors[emoji]) == 0 {
			delete(reactors, emoji)
		}
	})
	return m.RefreshMessage(event.ChannelID, event.MessageID)
}

// OnReactionsRemoved shows that an event removed every reaction with emoji from a message, or every reaction if emoji is empty.
func (m *Model) OnReactionsRemoved(channelID discord.ChannelID, id discord.MessageID, emoji discord.APIEmoji) tview.Cmd {
	m.editReactors(channelID, id, func(reactors map[discord.APIEmoji][]discord.User) {
		if emoji == "" {
			clear(reactors)
		} else {
			delete(reactors, emoji)
		}
	})
	return m.RefreshMessage(channelID, id)
}

// refetchReactors asks again who reacted to the message with id, if they are shown.
func (m Model) refetchReactors(channelID discord.ChannelID, id discord.MessageID) tview.Cmd {
	index := m.indexOf(channelID, id)
	if _, shown := m.reactors[id]; !shown || index == -1 {
		return nil
	}
	return m.fetchReactors(m.items[index].message, false)
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
