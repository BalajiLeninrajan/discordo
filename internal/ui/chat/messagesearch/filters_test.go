package messagesearch

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

func TestWord(t *testing.T) {
	tests := []struct {
		query      string
		cursor     int
		start, end int
	}{
		{"", 0, 0, 0},
		{"from:al", 7, 0, 7},
		{"hi from:al", 10, 3, 10},
		{"hi from:al there", 6, 3, 10},
		{"hi ", 3, 3, 3},
	}
	for _, test := range tests {
		if start, end := word(test.query, test.cursor); start != test.start || end != test.end {
			t.Errorf("word(%q, %d) = %d, %d, want %d, %d", test.query, test.cursor, start, end, test.start, test.end)
		}
	}
}

// inDM returns m searching a DM with alice, logged in as bob.
func inDM(t *testing.T, m Model) Model {
	t.Helper()
	if err := m.state.MyselfSet(discord.User{ID: 1, Username: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	m.Open(&discord.Channel{ID: 7, DMRecipients: []discord.User{{ID: 5, Username: "alice", DisplayName: "Alice A"}}})
	return m
}

// typed returns m with query typed and the cursor at its end.
func typed(m Model, query string) Model {
	m, _ = m.Update(queryMsg{query: query, cursor: len(query)})
	return m
}

func inserts(suggestions []suggestion) []string {
	var out []string
	for _, s := range suggestions {
		out = append(out, s.insert)
	}
	return out
}

func TestModelSuggestions(t *testing.T) {
	m := inDM(t, newTestModel(t))
	tests := []struct {
		query string
		want  string
	}{
		{"", "from: mentions: has: before: during: after:"},
		{"lunch fr", "from:"},
		{"lunch ", ""},
		{"has:im", "has:image"},
		{"from:ali", "from:alice"},
		{"mentions:bo", "mentions:bob"},
		{"before:" + time.Now().Format(dateLayout)[:4], "before:" + time.Now().Format(dateLayout)},
	}
	for _, test := range tests {
		if got := strings.Join(inserts(typed(m, test.query).suggestions()), " "); !strings.HasPrefix(got, test.want) || (test.want == "" && got != "") {
			t.Errorf("suggestions for %q = %q, want %q", test.query, got, test.want)
		}
	}
}

func TestModelComplete(t *testing.T) {
	m := inDM(t, newTestModel(t))

	t.Run("tab", func(t *testing.T) {
		got, _ := typed(m, "lunch fr").Update(completeMsg{})
		if got.query != "lunch from:" || got.queryCursor != len(got.query) {
			t.Fatalf("query = %q, cursor = %d", got.query, got.queryCursor)
		}
	})

	t.Run("enter completes an unfinished filter", func(t *testing.T) {
		got, command := typed(m, "from:ali").Update(submitMsg{})
		if got.query != "from:alice " || got.loading || command != nil {
			t.Fatalf("query = %q, loading = %v", got.query, got.loading)
		}
	})

	t.Run("enter searches a finished filter", func(t *testing.T) {
		got, command := typed(m, "from:alice lunch").Update(submitMsg{})
		if command == nil || !got.loading || !slices.Equal(got.data.AuthorIDs, []discord.UserID{5}) || got.data.Content != "lunch" {
			t.Fatalf("data = %+v, loading = %v", got.data, got.loading)
		}
	})

	t.Run("an unknown user is not searched for", func(t *testing.T) {
		got, command := typed(m, "from:carol").Update(submitMsg{})
		if command != nil || got.loading || !strings.Contains(got.status(), "carol") {
			t.Fatalf("status = %q, loading = %v", got.status(), got.loading)
		}
	})
}

func TestModelParse(t *testing.T) {
	m := newTestModel(t)
	guildID := m.channel.GuildID
	if err := m.state.MemberSet(guildID, &discord.Member{User: discord.User{ID: 5, Username: "alice"}, Nick: "Al"}, false); err != nil {
		t.Fatal(err)
	}

	data, err := m.parse("see you mentions:Al has:LINK during:2026-03-04")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 3, 4, 0, 0, 0, 0, time.Local)
	if data.Content != "see you" || !slices.Equal(data.Mentions, []discord.UserID{5}) || !slices.Equal(data.Has, []string{"link"}) || data.ChannelID != m.channel.ID {
		t.Fatalf("data = %+v", data)
	}
	if !data.MinID.Time().Equal(day) || !data.MaxID.Time().Equal(day.AddDate(0, 0, 1)) {
		t.Fatalf("during = %v to %v, want the day of %v", data.MinID.Time(), data.MaxID.Time(), day)
	}

	if data, _ := m.parse("before:2026-03-04"); !data.MaxID.Time().Equal(day) || data.MinID.IsValid() {
		t.Fatalf("before = %+v", data)
	}
	if data, _ := m.parse("after:2026-03-04"); !data.MinID.Time().Equal(day.AddDate(0, 0, 1)) || data.MaxID.IsValid() {
		t.Fatalf("after = %+v", data)
	}

	t.Run("repeated filters", func(t *testing.T) {
		if err := m.state.MemberSet(guildID, &discord.Member{User: discord.User{ID: 6, Username: "bob"}}, false); err != nil {
			t.Fatal(err)
		}
		data, err := m.parse("from:Al from:bob from:alice has:link has:image has:link before:2026-03-10 before:2026-03-04 after:2026-03-01 after:2026-03-02")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(data.AuthorIDs, []discord.UserID{5, 6}) || !slices.Equal(data.Has, []string{"link", "image"}) {
			t.Fatalf("data = %+v, want both authors and both kinds, once each", data)
		}
		if !data.MaxID.Time().Equal(day) || !data.MinID.Time().Equal(time.Date(2026, 3, 3, 0, 0, 0, 0, time.Local)) {
			t.Fatalf("dates = %v to %v, want where they overlap", data.MinID.Time(), data.MaxID.Time())
		}

		values, err := new(httputil.DefaultSchema).Encode(data)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(values["author_id"], []string{"5", "6"}) || !slices.Equal(values["has"], []string{"link", "image"}) || values.Has("mentions") {
			t.Fatalf("values = %v, want a parameter for each value", values)
		}
	})

	for _, query := range []string{"from:", "has:everything", "before:march", "from:nobody"} {
		if _, err := m.parse(query); err == nil {
			t.Errorf("parse(%q) took it", query)
		}
	}
}
