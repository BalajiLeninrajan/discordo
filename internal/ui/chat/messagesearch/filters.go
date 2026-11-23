package messagesearch

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/discordo/internal/ui"
	"github.com/ayn2op/discordo/internal/ui/autocomplete/mentions"
	"github.com/ayn2op/tview/text"
)

// searchData is what a search asks Discord for. Unlike api.SearchData, the filters Discord takes more than once are lists, each value sent as a parameter of its own.
type searchData struct {
	Offset  uint   `schema:"offset,omitempty"`
	Content string `schema:"content,omitempty"`
	// ChannelIDs are the channels searched, or none for the whole guild.
	ChannelIDs []discord.ChannelID `schema:"channel_id,omitempty"`
	AuthorIDs  []discord.UserID    `schema:"author_id,omitempty"`
	Mentions   []discord.UserID    `schema:"mentions,omitempty"`
	Has        []string            `schema:"has,omitempty"`
	MaxID      discord.MessageID   `schema:"max_id,omitempty"`
	MinID      discord.MessageID   `schema:"min_id,omitempty"`
	// anywhere reports whether in:any asks for the whole guild, whatever channels in: also names.
	anywhere bool `schema:"-"`
}

// addOnce appends value to values unless it is already there.
func addOnce[T comparable](values []T, value T) []T {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

// before narrows data to messages before id, keeping the earlier bound if there is one.
func (data *searchData) before(id discord.MessageID) {
	if !data.MaxID.IsValid() || id < data.MaxID {
		data.MaxID = id
	}
}

// after narrows data to messages from id on, keeping the later bound if there is one.
func (data *searchData) after(id discord.MessageID) {
	if id > data.MinID {
		data.MinID = id
	}
}

// filter is a search option typed as key:value, as in Discord's search, with a hint of what its value is.
type filter struct {
	key, hint string
}

var filters = []filter{
	{"from", "user"},
	{"mentions", "user"},
	{"in", "channel, or any for the server"},
	{"has", "link, file, image, video, embed, ..."},
	{"before", "date"},
	{"during", "date"},
	{"after", "date"},
}

// hasValues are what has: takes. A snapshot is a forwarded message.
var hasValues = []string{"link", "embed", "file", "image", "video", "sound", "sticker", "poll", "snapshot"}

// dateLayout is how dates are typed: year, month and day.
const dateLayout = "2006-01-02"

// maxUserSuggestions is how many users from: and mentions: suggest, and maxChannelSuggestions how many channels in: does.
const (
	maxUserSuggestions    = 10
	maxChannelSuggestions = 10
)

// anywhere is the value of in: that searches the whole server.
const anywhere = "any"

// suggestion completes the word the cursor is in.
type suggestion struct {
	// insert replaces the word, followed by a space if final, as it completes a value.
	insert string
	final  bool
	label  []text.Span
}

// word returns the byte offsets of the start and end of the word that the cursor at the byte offset cursor is in or at the end of.
func word(query string, cursor int) (start, end int) {
	cursor = min(max(cursor, 0), len(query))
	start = strings.LastIndexAny(query[:cursor], " \t") + 1
	end = len(query)
	if i := strings.IndexAny(query[cursor:], " \t"); i != -1 {
		end = cursor + i
	}
	return start, end
}

// filterOf returns the filter typed as word, and its value, or false if word is not one.
func filterOf(word string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(word, ":")
	if !ok || !slices.ContainsFunc(filters, func(f filter) bool { return f.key == key }) {
		return "", "", false
	}
	return key, value, true
}

// suggestions returns the completions of the word the cursor is in: the filters it starts, or the values of the filter it is.
// An empty query suggests every filter.
func (m Model) suggestions() []suggestion {
	start, end := word(m.query, m.queryCursor)
	current := m.query[start:end]
	key, value, ok := filterOf(current)
	if !ok {
		if current == "" && strings.TrimSpace(m.query) != "" {
			return nil
		}
		return m.filterSuggestions(current)
	}

	switch key {
	case "from", "mentions":
		return m.userSuggestions(key, value)
	case "in":
		return m.channelSuggestions(value)
	case "has":
		return valueSuggestions(key, value, hasValues, nil)
	default:
		today := time.Now().Format(dateLayout)
		yesterday := time.Now().AddDate(0, 0, -1).Format(dateLayout)
		return valueSuggestions(key, value, []string{today, yesterday}, []string{"today", "yesterday"})
	}
}

// filterSuggestions returns the filters whose keys start with prefix.
func (m Model) filterSuggestions(prefix string) []suggestion {
	theme := m.cfg.Theme.MessagesList
	var suggestions []suggestion
	for _, f := range filters {
		// A DM is the only channel there is to search.
		if f.key == "in" && (m.channel == nil || !m.channel.GuildID.IsValid()) {
			continue
		}
		if strings.HasPrefix(f.key, strings.ToLower(prefix)) {
			suggestions = append(suggestions, suggestion{insert: f.key + ":", label: []text.Span{
				{Text: f.key + ":", Style: theme.MentionStyle.Style},
				{Text: " " + f.hint, Style: theme.MessageStyle.Dim(true)},
			}})
		}
	}
	return suggestions
}

// valueSuggestions returns the values of the filter key that start with prefix, described by the notes at the same index, if any.
func valueSuggestions(key, prefix string, values, notes []string) []suggestion {
	var suggestions []suggestion
	for i, value := range values {
		if !strings.HasPrefix(value, strings.ToLower(prefix)) {
			continue
		}
		label := []text.Span{{Text: value}}
		if i < len(notes) {
			label = append(label, text.Span{Text: "  " + notes[i]})
		}
		suggestions = append(suggestions, suggestion{insert: key + ":" + value, final: true, label: label})
	}
	return suggestions
}

// userSuggestions returns the users of the channel matching name, or who wrote in it last if name is empty, for the filter key.
func (m Model) userSuggestions(key, name string) []suggestion {
	if m.channel == nil {
		return nil
	}
	items, _ := mentions.Items(m.cfg, m.state, m.channel, name, maxUserSuggestions)
	suggestions := make([]suggestion, 0, len(items))
	for _, item := range items {
		username := strings.TrimPrefix(item.Insert(), "@")
		label := append(item.Line(nil), text.Span{Text: " " + username, Style: m.cfg.Theme.MessagesList.MessageStyle.Dim(true)})
		suggestions = append(suggestions, suggestion{insert: key + ":" + username, final: true, label: label})
	}
	return suggestions
}

// channelSuggestions returns any, which searches the whole server, and the channels of the server whose names start with prefix.
func (m Model) channelSuggestions(prefix string) []suggestion {
	if m.channel == nil || !m.channel.GuildID.IsValid() {
		return nil
	}
	prefix = strings.ToLower(strings.TrimPrefix(prefix, "#"))
	dim := m.cfg.Theme.MessagesList.MessageStyle.Dim(true)

	var suggestions []suggestion
	if strings.HasPrefix(anywhere, prefix) {
		suggestions = append(suggestions, suggestion{insert: "in:" + anywhere, final: true, label: []text.Span{
			{Text: anywhere},
			{Text: "  the whole server", Style: dim},
		}})
	}
	for _, channel := range m.searchableChannels() {
		if len(suggestions) == maxChannelSuggestions {
			break
		}
		if strings.HasPrefix(strings.ToLower(channel.Name), prefix) {
			suggestions = append(suggestions, suggestion{insert: "in:" + channel.Name, final: true, label: []text.Span{{Text: "#" + channel.Name}}})
		}
	}
	return suggestions
}

// searchableChannels returns the channels of the server the user can see, in the order of the guilds tree.
// A channel whose name has a space, as a voice channel or thread can, cannot be typed as one word and is left out.
func (m Model) searchableChannels() []discord.Channel {
	channels, _ := m.state.Cabinet.Channels(m.channel.GuildID)
	channels = slices.DeleteFunc(slices.Clone(channels), func(channel discord.Channel) bool {
		return channel.Type == discord.GuildCategory ||
			strings.ContainsAny(channel.Name, " \t") ||
			!m.state.HasPermissions(channel.ID, discord.PermissionViewChannel)
	})
	ui.SortGuildChannels(channels)
	return channels
}

// complete replaces the word the cursor is in with s, moving the cursor after it.
func (m *Model) complete(s suggestion) {
	start, end := word(m.query, m.queryCursor)
	insert := s.insert
	if s.final {
		insert += " "
		// A space already after the word is kept rather than doubled.
		if end < len(m.query) && m.query[end] == ' ' {
			end++
		}
	}
	m.query = m.query[:start] + insert + m.query[end:]
	m.queryCursor = start + len(insert)
	m.suggestion = 0
}

// unfinished reports whether the cursor is in a filter whose value is not one the search takes.
func (m Model) unfinished() bool {
	start, end := word(m.query, m.queryCursor)
	_, _, ok := filterOf(m.query[start:end])
	if !ok {
		return false
	}
	var data searchData
	return m.applyFilter(&data, m.query[start:end]) != nil
}

// parse returns the search the query asks for: its filters, and the rest of it as the content.
// A filter typed more than once sends each value, which Discord combines as its own search does, and dates narrow the search to where they overlap.
func (m Model) parse(query string) (searchData, error) {
	var data searchData
	var content []string
	for _, word := range strings.Fields(query) {
		if _, _, ok := filterOf(word); !ok {
			content = append(content, word)
			continue
		}
		if err := m.applyFilter(&data, word); err != nil {
			return searchData{}, err
		}
	}
	data.Content = strings.Join(content, " ")
	// Without in:, the search is of the channel it was opened in.
	switch {
	case data.anywhere:
		data.ChannelIDs = nil
	case len(data.ChannelIDs) == 0:
		data.ChannelIDs = []discord.ChannelID{m.channel.ID}
	}
	return data, nil
}

// applyFilter sets what the filter typed as word asks for in data.
func (m Model) applyFilter(data *searchData, word string) error {
	key, value, _ := filterOf(word)
	if value == "" {
		return fmt.Errorf("%s: needs a %s", key, filterHint(key))
	}

	switch key {
	case "from", "mentions":
		userID, ok := m.findUser(value)
		if !ok {
			return fmt.Errorf("no user %s here", value)
		}
		if key == "from" {
			data.AuthorIDs = addOnce(data.AuthorIDs, userID)
		} else {
			data.Mentions = addOnce(data.Mentions, userID)
		}
	case "in":
		if !m.channel.GuildID.IsValid() {
			return fmt.Errorf("in: only works in a server")
		}
		name := strings.TrimPrefix(value, "#")
		if strings.EqualFold(value, anywhere) {
			data.anywhere = true
			break
		}
		// Channels of different categories may share a name, and each is searched.
		found := false
		for _, channel := range m.searchableChannels() {
			if strings.EqualFold(channel.Name, name) {
				data.ChannelIDs = addOnce(data.ChannelIDs, channel.ID)
				found = true
			}
		}
		if !found {
			return fmt.Errorf("no channel #%s here", name)
		}
	case "has":
		if !slices.Contains(hasValues, strings.ToLower(value)) {
			return fmt.Errorf("has: takes %s", strings.Join(hasValues, ", "))
		}
		data.Has = addOnce(data.Has, strings.ToLower(value))
	default:
		day, err := parseDate(value)
		if err != nil {
			return fmt.Errorf("%s: takes a date as YYYY-MM-DD", key)
		}
		start := discord.MessageID(discord.NewSnowflake(day))
		next := discord.MessageID(discord.NewSnowflake(day.AddDate(0, 0, 1)))
		switch key {
		case "before":
			data.before(start)
		case "after":
			data.after(next)
		case "during":
			data.after(start)
			data.before(next)
		}
	}
	return nil
}

func filterHint(key string) string {
	index := slices.IndexFunc(filters, func(f filter) bool { return f.key == key })
	return filters[index].hint
}

// parseDate returns the start of the day value names in local time: a date as YYYY-MM-DD, today or yesterday.
func parseDate(value string) (time.Time, error) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch strings.ToLower(value) {
	case "today":
		return midnight, nil
	case "yesterday":
		return midnight.AddDate(0, 0, -1), nil
	}
	return time.ParseInLocation(dateLayout, value, time.Local)
}

// findUser returns the ID of the user of the channel with the username name, or with it as their nickname or display name.
func (m Model) findUser(name string) (discord.UserID, bool) {
	var users []discord.User
	nicks := map[discord.UserID]string{}
	if m.channel.GuildID.IsValid() {
		members, _ := m.state.Cabinet.Members(m.channel.GuildID)
		for _, member := range members {
			users = append(users, member.User)
			nicks[member.User.ID] = member.Nick
		}
	} else {
		users = slices.Clone(m.channel.DMRecipients)
		if me, err := m.state.Cabinet.Me(); err == nil {
			users = append(users, *me)
		}
	}

	// A username is unique, so it is looked for first.
	for _, user := range users {
		if strings.EqualFold(user.Username, name) {
			return user.ID, true
		}
	}
	for _, user := range users {
		if strings.EqualFold(nicks[user.ID], name) || strings.EqualFold(user.DisplayName, name) {
			return user.ID, true
		}
	}
	return 0, false
}
