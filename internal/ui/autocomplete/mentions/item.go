// Package mentions completes mentions.
package mentions

import (
	"cmp"
	"log/slog"
	"slices"
	"strings"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/discordo/internal/config"
	"github.com/ayn2op/discordo/internal/ui/autocomplete"
	"github.com/ayn2op/discordo/internal/ui/images"
	"github.com/ayn2op/ningen/v3"
	"github.com/ayn2op/tview"
	"github.com/ayn2op/tview/cmd"
	tviewimage "github.com/ayn2op/tview/image"
	"github.com/ayn2op/tview/text"
	"github.com/gdamore/tcell/v3"
	"github.com/sahilm/fuzzy"
)

const (
	// Title is the autocomplete's title for mentions.
	Title     = "Mentions"
	separator = " "
	// avatarRows is one line, so avatars use their own URL instead of sharing the taller ones in the messages list.
	avatarRows = 1
	avatarSize = "?size=32"
)

type mention struct {
	// insert is the text that completes the mention, like "@name".
	insert, name string
	avatar       discord.URL
	style        tview.Style
}

func (m mention) Insert() string { return m.insert }

func (m mention) Line(images map[discord.URL]tviewimage.Widget) []text.Span {
	line := []text.Span{{Text: m.name, Style: m.style}}
	if image, ok := images[m.avatar]; ok {
		if lines := image.Lines(); len(lines) > 0 {
			line = slices.Concat(lines[0], []text.Span{{Text: separator}}, line)
		}
	}
	return line
}

// Items returns up to limit users and roles in channel matching query, best first, or its recent authors if query is empty, and the command that loads their avatars, or nil.
func Items(cfg *config.Config, s *ningen.State, channel *discord.Channel, query string, limit int) ([]autocomplete.Item, tview.Cmd) {
	var items []autocomplete.Item
	images := images.RequestedMsg{Squares: map[discord.URL]uint{}}
	for _, mention := range mentions(s, channel, query, limit) {
		items = append(items, mention)
		if mention.avatar != "" {
			images.Squares[mention.avatar] = avatarRows
		}
	}

	if !cfg.UI.Autocomplete.Mentions.Preview || len(images.Squares) == 0 {
		return items, nil
	}
	return items, cmd.Task(func() tview.Msg { return images })
}

func mentions(s *ningen.State, channel *discord.Channel, query string, limit int) []mention {
	if query == "" {
		return recentAuthors(s, channel, limit)
	}

	var items []mention
	if !channel.GuildID.IsValid() {
		// DMs have recipients, not members.
		me, _ := s.Cabinet.Me()
		users := append(slices.Clone(channel.DMRecipients), *me)
		for _, r := range fuzzy.FindFrom(query, userList(users)) {
			if len(items) == limit {
				break
			}

			items = append(items, userMention(s, &users[r.Index]))
		}
		return items
	}

	members, err := s.Cabinet.Members(channel.GuildID)
	if err != nil {
		slog.Error("failed to get members from state", "guild_id", channel.GuildID, "err", err)
		return nil
	}

	roles := MentionableRoles(s, channel)
	memberMatches, roleMatches := fuzzy.FindFrom(query, memberList(members)), fuzzy.FindFrom(query, roleList(roles))
	// Both match lists are sorted by score, so merging them keeps the best first.
	for len(items) < limit && (len(memberMatches) > 0 || len(roleMatches) > 0) {
		if len(roleMatches) > 0 && (len(memberMatches) == 0 || roleMatches[0].Score > memberMatches[0].Score) {
			items = append(items, roleMention(roles, roles[roleMatches[0].Index]))
			roleMatches = roleMatches[1:]
			continue
		}

		member := &members[memberMatches[0].Index]
		memberMatches = memberMatches[1:]
		if ChannelHasUser(s, channel.ID, member.User.ID) {
			items = append(items, memberMention(s, channel.GuildID, member))
		}
	}
	return items
}

func recentAuthors(s *ningen.State, channel *discord.Channel, limit int) []mention {
	messages, err := s.Cabinet.Messages(channel.ID)
	if err != nil {
		return nil
	}

	me, _ := s.Cabinet.Me()
	shown := map[discord.UserID]bool{me.ID: true}
	var items []mention
	for _, message := range messages {
		if len(items) == limit {
			break
		}

		if shown[message.Author.ID] {
			continue
		}

		shown[message.Author.ID] = true
		if !channel.GuildID.IsValid() {
			items = append(items, userMention(s, &message.Author))
			continue
		}

		s.MemberState.RequestMember(channel.GuildID, message.Author.ID)
		if member, err := s.Cabinet.Member(channel.GuildID, message.Author.ID); err == nil {
			items = append(items, memberMention(s, channel.GuildID, member))
		}
	}
	return items
}

func memberMention(s *ningen.State, gID discord.GuildID, member *discord.Member) mention {
	style := presenceStyle(s, gID, member.User.ID)
	// This avoids a slower member color lookup path.
	color, ok := state.MemberColor(member, func(id discord.RoleID) *discord.Role {
		r, _ := s.Cabinet.Role(gID, id)
		return r
	})
	if ok {
		style = style.Foreground(tcell.NewHexColor(int32(color)))
	}

	return mention{"@" + member.User.Username, cmp.Or(member.Nick, member.User.DisplayName, member.User.Username), avatarURL(member.User), style}
}

func userMention(s *ningen.State, user *discord.User) mention {
	return mention{"@" + user.Username, user.DisplayOrUsername(), avatarURL(*user), presenceStyle(s, discord.NullGuildID, user.ID)}
}

// roleMention inserts the role by its name, or by its raw mention if it has no RoleName or another role in roles shares it.
func roleMention(roles []discord.Role, role discord.Role) mention {
	name := RoleName(role)
	shared := slices.ContainsFunc(roles, func(other discord.Role) bool {
		return other.ID != role.ID && strings.EqualFold(RoleName(other), name)
	})
	insert := "@" + name
	if name == "" || shared {
		insert = role.ID.Mention()
	}

	style := tcell.StyleDefault
	if role.Color != discord.NullColor {
		style = style.Foreground(tcell.NewHexColor(int32(role.Color)))
	}
	return mention{insert: insert, name: "@" + role.Name, style: style}
}

// RoleName returns the name of role without the characters a mention cannot hold, as in "@AutoLead" for the role "Auto Lead", or "" if that is "everyone" or "here", which Discord parses as itself.
func RoleName(role discord.Role) string {
	name := strings.Map(func(r rune) rune {
		if isMentionRune(r) {
			return r
		}
		return -1
	}, role.Name)
	if strings.EqualFold(name, "everyone") || strings.EqualFold(name, "here") {
		return ""
	}
	return name
}

func isMentionRune(r rune) bool {
	return r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_')
}

// MentionableRoles returns the roles of the guild of channel that the current user may mention there: all of them with the Mention Everyone permission, otherwise the mentionable ones.
func MentionableRoles(s *ningen.State, channel *discord.Channel) []discord.Role {
	if !channel.GuildID.IsValid() {
		return nil
	}

	roles, err := s.Cabinet.Roles(channel.GuildID)
	if err != nil {
		slog.Error("failed to get roles from state", "guild_id", channel.GuildID, "err", err)
		return nil
	}

	var everyone bool
	if me, _ := s.Cabinet.Me(); me != nil {
		perms, err := s.Permissions(channel.ID, me.ID)
		everyone = err == nil && perms.Has(discord.PermissionMentionEveryone)
	}

	return slices.DeleteFunc(slices.Clone(roles), func(role discord.Role) bool {
		// The @everyone role shares the guild's ID and is mentioned as plain text.
		return discord.GuildID(role.ID) == channel.GuildID || !everyone && !role.Mentionable
	})
}

func avatarURL(user discord.User) discord.URL {
	return user.AvatarURLWithType(discord.PNGImage) + avatarSize
}

// presenceStyle dims offline users.
func presenceStyle(s *ningen.State, gID discord.GuildID, userID discord.UserID) tview.Style {
	presence, err := s.Cabinet.Presence(gID, userID)
	if err != nil {
		slog.Info("failed to get presence from state", "guild_id", gID, "user_id", userID, "err", err)
	}
	return tcell.StyleDefault.Dim(err == nil && presence.Status == discord.OfflineStatus)
}

// ChannelHasUser reports whether the user may view the channel.
func ChannelHasUser(s *ningen.State, channelID discord.ChannelID, userID discord.UserID) bool {
	perms, err := s.Permissions(channelID, userID)
	if err != nil {
		slog.Error("failed to get permissions", "err", err, "channel", channelID, "user", userID)
		return false
	}
	return perms.Has(discord.PermissionViewChannel)
}

type memberList []discord.Member

func (ml memberList) String(i int) string {
	return ml[i].Nick + ml[i].User.DisplayName + ml[i].User.Tag()
}

func (ml memberList) Len() int { return len(ml) }

type userList []discord.User

func (ul userList) String(i int) string { return ul[i].DisplayName + ul[i].Tag() }

func (ul userList) Len() int { return len(ul) }

type roleList []discord.Role

func (rl roleList) String(i int) string { return rl[i].Name }

func (rl roleList) Len() int { return len(rl) }
