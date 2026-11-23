package ui

import (
	"log/slog"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/ningen/v3"
)

// manualAck is the body of an ack that moves the read state back, as Discord's "Mark Unread" sends.
type manualAck struct {
	Manual       bool `json:"manual"`
	MentionCount int  `json:"mention_count"`
}

// BeforeMessage returns the cursor one millisecond before the message with id, which reads up to the messages before it.
// The read state is a cursor that need not be a message, so a channel can be read up to just before its first message.
func BeforeMessage(id discord.MessageID) discord.MessageID {
	return discord.MessageID(discord.NewSnowflake(id.Time().Add(-time.Millisecond)))
}

// MarkUnread marks the channel with channelID unread from the message with messageID on, as Discord's "Mark Unread" on
// that message does, with mentions as the number of mentions of the user from it on. It sends a request.
func MarkUnread(state *ningen.State, channelID discord.ChannelID, messageID discord.MessageID, mentions int) error {
	read := BeforeMessage(messageID)
	endpoint := api.EndpointChannels + channelID.String() + "/messages/" + read.String() + "/ack"
	if err := state.FastRequest("POST", endpoint, httputil.WithJSONBody(manualAck{Manual: true, MentionCount: mentions})); err != nil {
		return err
	}
	slog.Debug("marked channel unread", "channel_id", channelID, "message_id", messageID, "cursor", read, "mentions", mentions)

	// Discord's own ack of it may not reach this session, so the read state is moved back here too. That clears the
	// mentions, which are then added back, unless the newest message is the user's: ningen then takes the channel as
	// read up to it, undoing the ack.
	state.Call(&gateway.MessageAckEvent{ChannelID: channelID, MessageID: read})
	if last := state.LastMessage(channelID); mentions > 0 && !newestIsMine(state, channelID, last) {
		state.ReadState.MarkUnread(channelID, last, mentions)
	}
	return nil
}

// newestIsMine reports whether the message with id in the channel with channelID is known and the user wrote it.
func newestIsMine(state *ningen.State, channelID discord.ChannelID, id discord.MessageID) bool {
	message, err := state.Cabinet.Message(channelID, id)
	return err == nil && IsMe(state, message.Author.ID)
}
