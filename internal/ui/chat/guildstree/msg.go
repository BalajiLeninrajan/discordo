package guildstree

import (
	"log/slog"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
	"github.com/ayn2op/tview/tree"
)

// currentMsg makes a node of the tree the current one.
type currentMsg struct {
	current *tree.Node
}

// selectedMsg is sent when the user selects a node.
type selectedMsg struct {
	node *tree.Node
}

type ChannelLoadedMsg struct {
	Channel  discord.Channel
	Messages []discord.Message
}

type NavigateMsg struct{ ChannelID discord.ChannelID }

// VoiceMsg is sent when who is speaking or the state of the voice channel joined changes.
type VoiceMsg struct{}

func (m *Model) navigate(channelID discord.ChannelID) tview.Cmd {
	channel, err := m.state.Cabinet.Channel(channelID)
	if err != nil {
		slog.Error("failed to get channel from state", "err", err, "channel_id", channelID)
		return nil
	}
	node := m.findNodeByChannelID(channelID)
	if node == nil {
		slog.Error("failed to locate channel in tree", "channel_id", channelID)
		return nil
	}
	m.expandPathToNode(node)
	m.setCurrentNode(node)
	if channel.Type == discord.GuildCategory {
		return nil
	}
	return m.selectNode(node)
}

func (m Model) selectNode(node *tree.Node) tview.Cmd {
	if m.expandable(node) {
		node.SetExpanded(!node.Expanded())
		return nil
	}

	switch ref := node.Reference().(type) {
	case discord.GuildID:
		m.expandGuild(node, ref)
	case discord.ChannelID:
		return m.selectChannel(node, ref)
	case dmNode:
		m.expandPrivateChannels(node)
	}

	return nil
}

func (m Model) expandGuild(node *tree.Node, guildID discord.GuildID) {
	go m.state.MemberState.Subscribe(guildID)
	channels, err := m.state.Cabinet.Channels(guildID)
	if err != nil {
		slog.Error("failed to get channels", "err", err, "guild_id", guildID)
		return
	}

	ui.SortGuildChannels(channels)
	m.createChannelNodes(node, channels)
	m.showVoiceMembers(guildID)
	node.Expand()
}

// selectChannel loads the messages of the channel, or shows the threads of a forum.
func (m Model) selectChannel(node *tree.Node, channelID discord.ChannelID) tview.Cmd {
	channel, err := m.state.Cabinet.Channel(channelID)
	if err != nil {
		slog.Error("failed to get channel from state", "err", err, "channel_id", channelID)
		return nil
	}

	if channel.Type != discord.GuildForum {
		return loadChannel(m.state, uint(m.cfg.MessagesLimit), *channel)
	}

	m.expandForum(node, *channel)
	return nil
}

func (m Model) expandForum(node *tree.Node, forum discord.Channel) {
	channels, err := m.state.Cabinet.Channels(forum.GuildID)
	if err != nil {
		slog.Error("failed to get channels for forum threads", "err", err, "guild_id", forum.GuildID)
		return
	}

	for _, child := range channels {
		if child.ParentID == forum.ID && isThread(child.Type) {
			m.createChannelNode(node, child)
		}
	}

	node.Expand()
}

func (m Model) expandPrivateChannels(node *tree.Node) {
	channels, err := m.state.PrivateChannels()
	if err != nil {
		slog.Error("failed to get private channels", "err", err)
		return
	}

	ui.SortPrivateChannels(channels)
	for _, channel := range channels {
		m.createChannelNode(node, channel)
	}

	node.Expand()
}

func loadChannel(state *ningen.State, limit uint, channel discord.Channel) tview.Cmd {
	return cmd.Task(func() tview.Msg {
		messages, err := state.Messages(channel.ID, limit)
		if err != nil {
			slog.Error("failed to get messages", "err", err, "channel_id", channel.ID, "limit", limit)
			return nil
		}

		if lastMessageID := state.LastMessage(channel.ID); lastMessageID.IsValid() {
			go state.ReadState.MarkRead(channel.ID, lastMessageID)
		}

		return ChannelLoadedMsg{Channel: channel, Messages: messages}
	})
}

// markReadInterval is the time between the acknowledgements of several channels, which take a request each.
const markReadInterval = 50 * time.Millisecond

// unreadChannels returns the channels under node with unread messages or mentions: the channel itself, or those of the category, guild or direct messages it is.
func (m Model) unreadChannels(node *tree.Node) []discord.Channel {
	if node == nil {
		return nil
	}

	var channels []discord.Channel
	switch ref := node.Reference().(type) {
	case discord.GuildID:
		channels, _ = m.state.Cabinet.Channels(ref)
	case dmNode:
		channels, _ = m.state.PrivateChannels()
	case discord.ChannelID:
		channel, err := m.state.Cabinet.Channel(ref)
		if err != nil {
			return nil
		}
		if channel.Type != discord.GuildCategory {
			channels = []discord.Channel{*channel}
			break
		}

		siblings, _ := m.state.Cabinet.Channels(channel.GuildID)
		for _, sibling := range siblings {
			if sibling.ParentID == channel.ID {
				channels = append(channels, sibling)
			}
		}
	}

	var unread []discord.Channel
	for _, channel := range channels {
		if m.hasUnread(channel) {
			unread = append(unread, channel)
		}
	}
	return unread
}

// hasUnread reports whether channel has messages or mentions the user has not read. Unlike the style of its node, it counts a muted channel, as marking it read is asked for.
func (m Model) hasUnread(channel discord.Channel) bool {
	state := m.state.ReadState.ReadState(channel.ID)
	if state == nil {
		return false
	}
	if state.MentionCount > 0 {
		return true
	}

	last := m.state.LastMessage(channel.ID)
	if !last.IsValid() || state.LastMessageID >= last {
		return false
	}
	return !channel.GuildID.IsValid() || m.state.HasPermissions(channel.ID, discord.PermissionViewChannel)
}

// markRead marks the unread channels under node read up to their newest message.
func (m Model) markRead(node *tree.Node) tview.Cmd {
	channels := m.unreadChannels(node)
	if len(channels) == 0 {
		return nil
	}

	return cmd.Task(func() tview.Msg {
		for index, channel := range channels {
			if index > 0 {
				time.Sleep(markReadInterval)
			}
			m.state.ReadState.MarkRead(channel.ID, m.state.LastMessage(channel.ID))
		}
		return nil
	})
}
