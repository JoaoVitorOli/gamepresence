package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/egoist/mygo"

	"gamepresence/internal/procs"
)

// Backend is what the window's view calls: settings, games, and the
// presence watcher.
type Backend struct {
	store    *Store
	presence *Presence
	igdb     igdbClient
}

func NewBackend(store *Store, onStatus func(Status)) *Backend {
	b := &Backend{store: store}
	b.presence = NewPresence(store, onStatus)
	return b
}

func (b *Backend) GetStatus() Status { return b.presence.Status() }

func (b *Backend) GetSettings() Settings { return b.store.Settings() }

func (b *Backend) SaveSettings(s Settings) (Settings, error) {
	s.DiscordAppID = strings.TrimSpace(s.DiscordAppID)
	s.TwitchClientID = strings.TrimSpace(s.TwitchClientID)
	s.TwitchClientSecret = strings.TrimSpace(s.TwitchClientSecret)
	s.ImgurClientID = strings.TrimSpace(s.ImgurClientID)
	for _, r := range s.DiscordAppID {
		if r < '0' || r > '9' {
			return b.store.Settings(), errors.New("the Discord Application ID is the number shown in the Developer Portal")
		}
	}
	if err := b.store.SetSettings(s); err != nil {
		return b.store.Settings(), err
	}
	log.Printf("settings saved")
	b.presence.Kick()
	return b.store.Settings(), nil
}

func (b *Backend) GetGames() []Game { return b.store.Games() }

func newID() string {
	r := make([]byte, 8)
	rand.Read(r)
	return hex.EncodeToString(r)
}

func (b *Backend) SaveGame(g Game) (Game, error) {
	g.Name = strings.TrimSpace(g.Name)
	g.Exe = strings.TrimSpace(g.Exe)
	g.MatchText = strings.TrimSpace(g.MatchText)
	g.State = strings.TrimSpace(g.State)
	g.ImageURL = strings.TrimSpace(g.ImageURL)
	switch {
	case g.Name == "":
		return g, errors.New("enter the game's name")
	case len(g.Name) > 128 || len(g.State) > 128:
		return g, errors.New("name and status line are limited to 128 characters")
	case g.Exe == "":
		return g, errors.New("pick the game's process (exe)")
	}
	if g.ImageURL != "" {
		if err := validImageURL(g.ImageURL); err != nil {
			return g, err
		}
	}
	if g.MatchMode != "cmdline" && g.MatchMode != "title" {
		g.MatchMode = ""
	}
	games := b.store.Games()
	found := false
	for i := range games {
		if g.ID != "" && games[i].ID == g.ID {
			games[i] = g
			found = true
		}
	}
	if !found {
		if g.ID == "" {
			g.ID = newID()
		}
		games = append(games, g)
	}
	if err := b.store.SetGames(games); err != nil {
		return g, err
	}
	log.Printf("game saved: %s (%s)", g.Name, g.Exe)
	b.presence.Kick()
	return g, nil
}

func (b *Backend) DeleteGame(id string) error {
	var out []Game
	for _, g := range b.store.Games() {
		if g.ID != id {
			out = append(out, g)
		}
	}
	err := b.store.SetGames(out)
	b.presence.Kick()
	return err
}

// MoveGame changes priority: earlier games win when several are running.
func (b *Backend) MoveGame(id string, delta int) error {
	games := b.store.Games()
	for i := range games {
		if games[i].ID == id {
			j := i + delta
			if j < 0 || j >= len(games) {
				return nil
			}
			games[i], games[j] = games[j], games[i]
			break
		}
	}
	err := b.store.SetGames(games)
	b.presence.Kick()
	return err
}

func (b *Backend) SetGameEnabled(id string, on bool) error {
	games := b.store.Games()
	for i := range games {
		if games[i].ID == id {
			games[i].Enabled = on
		}
	}
	err := b.store.SetGames(games)
	b.presence.Kick()
	return err
}

// ListProcesses returns running processes, those with a window first.
func (b *Backend) ListProcesses() ([]procs.Process, error) {
	list, err := procs.List()
	if err != nil {
		return nil, err
	}
	self := uint32(os.Getpid())
	out := make([]procs.Process, 0, len(list))
	for _, p := range list {
		if p.PID != self {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		wi, wj := out[i].Title != "", out[j].Title != ""
		if wi != wj {
			return wi
		}
		return strings.ToLower(out[i].Exe) < strings.ToLower(out[j].Exe)
	})
	return out, nil
}

func (b *Backend) SearchIGDB(query string) ([]IGDBResult, error) {
	s := b.store.Settings()
	return b.igdb.Search(s.TwitchClientID, b.store.Secrets().TwitchClientSecret, query)
}

// PickImageFile asks for a local image and returns its path and bytes, or
// an empty path when the user cancels.
func (b *Backend) PickImageFile(parent *mygo.Window) (string, []byte, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:  parent,
		Title:   "Choose a cover image",
		Filters: []mygo.FileFilter{{Name: "Images", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp"}}},
	})
	if err != nil || len(paths) == 0 {
		return "", nil, err
	}
	data, _, err := readImage(paths[0])
	if err != nil {
		return "", nil, err
	}
	return paths[0], data, nil
}

// UploadImage publishes a local file to the configured host and returns
// its public URL.
func (b *Backend) UploadImage(path string) (UploadResult, error) {
	var (
		r   UploadResult
		err error
	)
	if b.store.Settings().UploadHost == "catbox" {
		r, err = uploadCatbox(path)
	} else {
		r, err = uploadImgur(b.store.Secrets().ImgurClientID, path)
	}
	if err != nil {
		log.Printf("upload failed: %v", err)
		return r, err
	}
	log.Printf("image uploaded: %s", r.URL)
	return r, nil
}

// DeleteUpload removes an image previously uploaded to Imgur.
func (b *Backend) DeleteUpload(deleteHash string) error {
	return deleteImgur(b.store.Secrets().ImgurClientID, deleteHash)
}

func (b *Backend) SetPaused(v bool)   { b.presence.SetPaused(v) }
func (b *Backend) TestGame(id string) { b.presence.SetTest(id) }
func (b *Backend) StopTest()          { b.presence.SetTest("") }

func (b *Backend) GetAutostart() bool { return mygo.App.OpenAtLogin() }

func (b *Backend) SetAutostart(on bool) error {
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		return err
	}
	log.Printf("start with Windows: %v", on)
	return nil
}

func (b *Backend) OpenConfigFolder() error { return mygo.Shell.OpenPath(configDir()) }
