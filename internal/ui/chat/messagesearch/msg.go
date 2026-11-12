package messagesearch

import (
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/tview/viewport"
)

// Msg is implemented by the messages the search sends itself, which must reach it even if it was closed while they were on their way.
type Msg interface{ isMsg() }

// SelectedMsg asks to show the message with MessageID in ChannelID.
type SelectedMsg struct {
	ChannelID discord.ChannelID
	MessageID discord.MessageID
}

type CancelMsg struct{}

// resultsMsg is a page of the results of search number request, starting at offset, or why it failed.
type resultsMsg struct {
	request int
	offset  uint
	results []discord.Message
	total   uint
	err     error
}

func (resultsMsg) isMsg() {}

// queryMsg is the query and the byte offset of the cursor in it that typing left.
type queryMsg struct {
	query  string
	cursor int
}

// cursorMsg is the result to select.
type cursorMsg int

// scrollMsg scrolls the results.
type scrollMsg viewport.ScrollState

// submitMsg completes the filter the cursor is in if it is unfinished, or else searches for the query, or shows the selected result if it is what the results are for.
type submitMsg struct{}

// suggestionMsg is the suggestion to select.
type suggestionMsg int

// completeMsg completes the word the cursor is in with the selected suggestion.
type completeMsg struct{}
