package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
)

type Game struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Exe         string `json:"exe"`
	MatchMode   string `json:"matchMode"` // "", "cmdline", "title"
	MatchText   string `json:"matchText"`
	State       string `json:"state"` // optional second line
	ImageURL    string `json:"imageUrl"`
	ImageSource string `json:"imageSource"` // "igdb", "url", "upload"
	IGDBID      int    `json:"igdbId,omitempty"`
	DeleteHash  string `json:"deleteHash,omitempty"` // Imgur delete hash for uploads
	Enabled     bool   `json:"enabled"`
}

// Settings as the UI sees them. Secret values never go to the UI, only
// whether they are set; they live in Windows Credential Manager.
type Settings struct {
	DiscordAppID       string `json:"discordAppId"`
	ShowGameInStatus   bool   `json:"showGameInStatus"` // status_display_type = details
	ShowElapsed        bool   `json:"showElapsed"`
	TwitchClientID     string `json:"twitchClientId"`
	UploadHost         string `json:"uploadHost"` // "imgur" or "catbox"
	TwitchSecretSet    bool   `json:"twitchSecretSet"`
	ImgurClientIDSet   bool   `json:"imgurClientIdSet"`
	TwitchClientSecret string `json:"twitchClientSecret,omitempty"` // input only
	ImgurClientID      string `json:"imgurClientId,omitempty"`      // input only
}

const (
	secretTwitch = "twitch-client-secret"
	secretImgur  = "imgur-client-id"
)

type secrets struct {
	TwitchClientSecret string
	ImgurClientID      string
}

type diskConfig struct {
	Settings Settings `json:"settings"`
	Games    []Game   `json:"games"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	settings Settings
	secrets  secrets
	games    []Game
}

// configDir is %APPDATA%\Game Presence.
func configDir() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		dir, _ = os.UserConfigDir()
		dir = filepath.Join(dir, appName)
	}
	return dir
}

func readSecret(key string) string {
	b, err := mygo.Secrets.Get(key)
	if err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
		log.Printf("could not read %s from Credential Manager: %v", key, err)
	}
	return string(b)
}

func writeSecret(key, value string) error {
	if value == "" {
		return mygo.Secrets.Delete(key)
	}
	return mygo.Secrets.Set(key, []byte(value))
}

func LoadStore() (*Store, error) {
	s := &Store{path: filepath.Join(configDir(), "config.json")}
	s.settings = Settings{ShowGameInStatus: true, ShowElapsed: true, UploadHost: "imgur"}
	s.secrets.TwitchClientSecret = readSecret(secretTwitch)
	s.secrets.ImgurClientID = readSecret(secretImgur)
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var d diskConfig
	if err := json.Unmarshal(b, &d); err != nil {
		return s, err
	}
	s.settings = d.Settings
	s.games = d.Games
	return s, nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	st := s.settings
	st.TwitchClientSecret, st.ImgurClientID = "", ""
	st.TwitchSecretSet, st.ImgurClientIDSet = false, false
	d := diskConfig{Settings: st, Games: s.games}
	if d.Games == nil {
		d.Games = []Game{}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.settings
	st.TwitchSecretSet = s.secrets.TwitchClientSecret != ""
	st.ImgurClientIDSet = s.secrets.ImgurClientID != ""
	st.TwitchClientSecret, st.ImgurClientID = "", ""
	return st
}

func (s *Store) Secrets() secrets {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secrets
}

// SetSettings stores new settings. Empty secret inputs keep the stored
// value; the special value "-" clears it.
func (s *Store) SetSettings(in Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	update := func(key, input string, cur *string) error {
		switch input {
		case "":
			return nil
		case "-":
			input = ""
		}
		if err := writeSecret(key, input); err != nil {
			return err
		}
		*cur = input
		return nil
	}
	if err := update(secretTwitch, in.TwitchClientSecret, &s.secrets.TwitchClientSecret); err != nil {
		return err
	}
	if err := update(secretImgur, in.ImgurClientID, &s.secrets.ImgurClientID); err != nil {
		return err
	}
	if in.UploadHost != "catbox" {
		in.UploadHost = "imgur"
	}
	s.settings = in
	return s.saveLocked()
}

func (s *Store) Games() []Game {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Game(nil), s.games...)
}

func (s *Store) SetGames(g []Game) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games = g
	return s.saveLocked()
}
