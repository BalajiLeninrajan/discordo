package ui

import (
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestBeforeMessage(t *testing.T) {
	id := discord.MessageID(discord.NewSnowflake(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)))
	if got := BeforeMessage(id); got >= id || id.Time().Sub(got.Time()) != time.Millisecond {
		t.Errorf("BeforeMessage = %v at %v, want a millisecond before %v", got, got.Time(), id.Time())
	}
}
