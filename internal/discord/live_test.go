package discord

import (
	"os"
	"testing"
	"time"
)

// Run with DISCORD_APP_ID set; it shows a test activity for a few seconds.
func TestLiveSetActivity(t *testing.T) {
	id := os.Getenv("DISCORD_APP_ID")
	if id == "" {
		t.Skip("DISCORD_APP_ID not set")
	}
	c, err := Dial(id)
	if err != nil {
		t.Fatal(err)
	}
	v := DisplayDetails
	err = c.SetActivity(&Activity{Type: TypePlaying, Details: "Bloodborne", State: "Game Presence test",
		Assets:            &Assets{LargeImage: "https://images.igdb.com/igdb/image/upload/t_cover_big/cob99l.jpg", LargeText: "Bloodborne"},
		Timestamps:        &Timestamps{Start: time.Now().UnixMilli()},
		StatusDisplayType: &v})
	if err != nil {
		t.Fatal("set:", err)
	}
	t.Logf("activity set as %s", c.User.Username)
	time.Sleep(8 * time.Second)
	if err := c.SetActivity(nil); err != nil {
		t.Fatal("clear:", err)
	}
	t.Log("cleared")
	c.Close()
}
