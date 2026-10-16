package main

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// testView builds a view on a store in a temporary directory, never the
// user's config, with questions answered by answer.
func testView(t *testing.T, games []Game, answer int) (*view, *ui.Tester, *atomic.Int32) {
	t.Helper()
	store := &Store{path: filepath.Join(t.TempDir(), "config.json"), settings: Settings{DiscordAppID: "1", ShowElapsed: true}}
	if err := store.SetGames(games); err != nil {
		t.Fatal(err)
	}
	b := NewBackend(store, func(Status) {})
	v := newView(b, nil, false)
	asked := &atomic.Int32{}
	v.ask = func(mygo.MessageOptions) int { asked.Add(1); return answer }
	return v, ui.NewTester(v.render, 1000, 720), asked
}

var bb = Game{ID: "bb", Name: "Bloodborne", Exe: "bb-probe.exe", Enabled: true}

func TestEditOpensEditorNotDelete(t *testing.T) {
	v, tt, asked := testView(t, []Game{bb}, 0)
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	if !v.editing || asked.Load() != 0 {
		t.Fatalf("editing=%v, questions asked=%d", v.editing, asked.Load())
	}
	if v.draft.Name != "Bloodborne" {
		t.Fatalf("draft %+v", v.draft)
	}
}

func TestDeleteNeedsConfirmation(t *testing.T) {
	_, tt, asked := testView(t, []Game{bb}, 1) // the user picks Cancel
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return asked.Load() == 1 })
	tt.Frame()
	if !tt.HasText("Bloodborne") {
		t.Fatal("game removed although the user canceled")
	}
}

func TestAddGameSaves(t *testing.T) {
	v, tt, _ := testView(t, nil, 0)
	if err := tt.Click("+ Add game"); err != nil {
		t.Fatal(err)
	}
	v.draft.Exe, v.draft.Name = "test.exe", "Test Game"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if v.editing || len(v.games) != 1 || v.games[0].Name != "Test Game" {
		t.Fatalf("editing=%v games=%+v err=%q", v.editing, v.games, v.editorErr)
	}
}

func TestTabs(t *testing.T) {
	_, tt, _ := testView(t, nil, 0)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Application ID") {
		t.Fatal("settings page not shown")
	}
	if err := tt.Click("Setup guide"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Getting started") {
		t.Fatal("guide not shown")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000 && !cond(); i++ {
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition never became true")
	}
}
