package ui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
)

// ChannelToString returns how channel is shown in the UI: an icon for its type and its name, or the recipients' names for a DM.
func ChannelToString(channel discord.Channel, icons config.Icons, state *ningen.State) string {
	var icon string
	switch channel.Type {
	case discord.DirectMessage, discord.GroupDM:
		if channel.Name != "" {
			return channel.Name
		}

		recipients := make([]string, len(channel.DMRecipients))
		for index, recipient := range channel.DMRecipients {
			recipients[index] = recipientName(recipient, channel.Type, state)
		}

		return strings.Join(recipients, ", ")

	case discord.GuildCategory:
		icon = icons.GuildCategory
	case discord.GuildText:
		icon = icons.GuildText
	case discord.GuildVoice:
		icon = icons.GuildVoice
	case discord.GuildStageVoice:
		icon = icons.GuildStageVoice

	case discord.GuildAnnouncementThread:
		icon = icons.GuildAnnouncementThread
	case discord.GuildPublicThread:
		icon = icons.GuildPublicThread
	case discord.GuildPrivateThread:
		icon = icons.GuildPrivateThread

	case discord.GuildAnnouncement:
		icon = icons.GuildAnnouncement
	case discord.GuildForum:
		icon = icons.GuildForum
	case discord.GuildStore:
		icon = icons.GuildStore
	}

	return icon + channel.Name
}

// recipientName returns the nickname given to a friend in a DM, or else the recipient's display name.
func recipientName(recipient discord.User, channelType discord.ChannelType, state *ningen.State) string {
	if state == nil || channelType != discord.DirectMessage {
		return recipient.DisplayOrUsername()
	}

	relationship, ok := state.RelationshipState.FullRelationship(recipient.ID)
	if !ok || relationship.Type != discord.FriendRelationship || relationship.Nickname == nil || *relationship.Nickname == "" {
		return recipient.DisplayOrUsername()
	}

	return *relationship.Nickname
}

// SortGuildChannels sorts channels by position, and threads, which have none, by latest activity first.
func SortGuildChannels(channels []discord.Channel) {
	slices.SortStableFunc(channels, func(a, b discord.Channel) int {
		if order := cmp.Compare(a.Position, b.Position); order != 0 {
			return order
		}
		if aThread, bThread := a.Type.IsThread(), b.Type.IsThread(); aThread != bThread {
			// A thread goes after the channels of its position.
			if aThread {
				return 1
			}
			return -1
		} else if !aThread {
			return 0
		}
		return cmp.Compare(lastMessageID(b), lastMessageID(a))
	})
}

// SortPrivateChannels sorts channels by latest activity first.
func SortPrivateChannels(channels []discord.Channel) {
	slices.SortFunc(channels, func(a, b discord.Channel) int {
		return cmp.Compare(lastMessageID(b), lastMessageID(a))
	})
}

// lastMessageID falls back to the channel ID, which is when the channel was created.
func lastMessageID(channel discord.Channel) discord.MessageID {
	if channel.LastMessageID.IsValid() {
		return channel.LastMessageID
	}

	return discord.MessageID(channel.ID)
}

// UnreadStyle returns the style of the name of something read, unread, or with unread mentions, as indication says.
func UnreadStyle(indication ningen.UnreadIndication) tview.Style {
	var style tview.Style
	switch indication {
	case ningen.ChannelRead:
		style = style.Dim(true)
	case ningen.ChannelMentioned:
		style = style.Underline(true)
		fallthrough
	case ningen.ChannelUnread:
		style = style.Bold(true)
	}
	return style
}
