package forumposts

import (
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/tview/viewport"
)

// Msg is implemented by the messages the posts send themselves, which must reach them even if they were closed while
// the messages were on their way.
type Msg interface{ isMsg() }

// SelectedMsg asks to open Post, a thread of the forum.
type SelectedMsg struct{ Post discord.Channel }

// NewPostMsg asks for the first message of a post in Forum titled Title, with Tags applied.
type NewPostMsg struct {
	Forum discord.Channel
	Title string
	Tags  []discord.TagID
}

// CancelMsg asks to close the posts.
type CancelMsg struct{}

// resultsMsg is a page of the posts that search number request found, starting at offset, or why it failed. If
// retryAfter is set, Discord has not indexed the forum yet and asks to search again after it.
type resultsMsg struct {
	request    int
	offset     int
	response   api.SearchThreadsResponse
	retryAfter time.Duration
	err        error
}

func (resultsMsg) isMsg() {}

// retryMsg searches again for page offset of search number request, which found the forum not indexed yet.
type retryMsg struct {
	request int
	offset  int
}

func (retryMsg) isMsg() {}

// freshMsg is the posts of the forum with forumID that are newer than it was read up to, of which there are more if
// more, or why they were not found.
type freshMsg struct {
	forumID discord.ChannelID
	posts   []discord.Channel
	more    bool
	err     error
}

func (freshMsg) isMsg() {}

// queryMsg is the query and the byte offset of the cursor in it that typing left.
type queryMsg struct {
	query  string
	cursor int
}

// cursorMsg is the post, or the tag while one is picked, to select.
type cursorMsg int

// scrollMsg scrolls the posts.
type scrollMsg viewport.ScrollState

// submitMsg searches for the query, opens the selected post if the posts are for the query, or toggles the selected
// tag of a new post.
type submitMsg struct{}

// cancelMsg goes back from picking a tag, or else closes the posts.
type cancelMsg struct{}

// newPostMsg starts a post titled with the query.
type newPostMsg struct{}
