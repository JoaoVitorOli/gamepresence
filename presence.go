package main

import (
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"gamepresence/internal/discord"
	"gamepresence/internal/procs"
)

const pollInterval = 5 * time.Second

type Status struct {
	DiscordConnected bool   `json:"discordConnected"`
	DiscordUser      string `json:"discordUser"`
	DiscordError     string `json:"discordError"`
	Paused           bool   `json:"paused"`
	Testing          bool   `json:"testing"`
	GameID           string `json:"gameId"`
	GameName         string `json:"gameName"`
	ImageURL         string `json:"imageUrl"`
	PID              uint32 `json:"pid"`
	StartMs          int64  `json:"startMs"`
}

// Presence watches processes and keeps Discord's activity in sync.
type Presence struct {
	store    *Store
	onChange func(Status)
	kick     chan struct{}

	mu      sync.Mutex
	status  Status
	paused  bool
	testID  string
	testAt  int64
	client  *discord.Client
	appID   string
	sent    string // JSON of the last activity sent, "" = cleared
	lastErr string
}

func NewPresence(store *Store, onChange func(Status)) *Presence {
	return &Presence{store: store, onChange: onChange, kick: make(chan struct{}, 1)}
}

// Kick re-evaluates immediately (after config changes).
func (p *Presence) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

func (p *Presence) Run(stop <-chan struct{}) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		p.tick()
		select {
		case <-stop:
			return
		case <-t.C:
		case <-p.kick:
		}
	}
}

func (p *Presence) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Presence) SetPaused(v bool) {
	p.mu.Lock()
	p.paused = v
	p.mu.Unlock()
	p.Kick()
}

// SetTest forces a game to be shown even if its process isn't running
// ("" stops the test).
func (p *Presence) SetTest(id string) {
	p.mu.Lock()
	p.testID = id
	p.testAt = time.Now().UnixMilli()
	p.mu.Unlock()
	p.Kick()
}

// Shutdown clears the activity and closes the pipe.
func (p *Presence) Shutdown() {
	p.mu.Lock()
	c := p.client
	p.client = nil
	p.mu.Unlock()
	if c != nil {
		c.SetActivity(nil)
		c.Close()
	}
}

type match struct {
	game Game
	proc procs.Process
}

// findRunning returns the first enabled game (in list order) with a
// matching process.
func findRunning(games []Game) *match {
	list, err := procs.Snapshot()
	if err != nil {
		log.Printf("process snapshot failed: %v", err)
		return nil
	}
	var titles map[uint32]string
	for _, g := range games {
		if !g.Enabled || g.Exe == "" {
			continue
		}
		needle := strings.ToLower(strings.TrimSpace(g.MatchText))
		for _, pr := range list {
			if !procs.SameExe(pr.Exe, g.Exe) {
				continue
			}
			procs.Fill(&pr)
			if needle != "" {
				switch g.MatchMode {
				case "title":
					if titles == nil {
						titles = procs.WindowTitles()
					}
					if !strings.Contains(strings.ToLower(titles[pr.PID]), needle) {
						continue
					}
				case "cmdline":
					if !strings.Contains(strings.ToLower(pr.CommandLine), needle) {
						continue
					}
				}
			}
			return &match{game: g, proc: pr}
		}
	}
	return nil
}

func (p *Presence) tick() {
	settings := p.store.Settings()
	games := p.store.Games()

	p.mu.Lock()
	paused, testID, testAt := p.paused, p.testID, p.testAt
	p.mu.Unlock()

	var m *match
	if testID != "" {
		for _, g := range games {
			if g.ID == testID {
				m = &match{game: g, proc: procs.Process{StartMs: testAt}}
			}
		}
	}
	if m == nil && !paused {
		m = findRunning(games)
	}

	var act *discord.Activity
	if m != nil {
		act = buildActivity(settings, m)
	}

	p.sync(settings.DiscordAppID, act)

	p.mu.Lock()
	st := Status{Paused: p.paused, Testing: p.testID != "", DiscordError: p.lastErr}
	if p.client != nil {
		st.DiscordConnected = true
		st.DiscordUser = p.client.User.GlobalName
		if st.DiscordUser == "" {
			st.DiscordUser = p.client.User.Username
		}
	}
	if m != nil {
		st.GameID, st.GameName, st.ImageURL = m.game.ID, m.game.Name, m.game.ImageURL
		st.PID, st.StartMs = m.proc.PID, m.proc.StartMs
	}
	changed := st != p.status
	p.status = st
	p.mu.Unlock()
	if changed && p.onChange != nil {
		p.onChange(st)
	}
}

func buildActivity(s Settings, m *match) *discord.Activity {
	a := &discord.Activity{Type: discord.TypePlaying, Details: m.game.Name}
	if st := strings.TrimSpace(m.game.State); st != "" {
		a.State = st
	}
	if m.game.ImageURL != "" {
		a.Assets = &discord.Assets{LargeImage: m.game.ImageURL, LargeText: m.game.Name}
	}
	if s.ShowElapsed && m.proc.StartMs > 0 {
		a.Timestamps = &discord.Timestamps{Start: m.proc.StartMs}
	}
	if s.ShowGameInStatus {
		v := discord.DisplayDetails
		a.StatusDisplayType = &v
	}
	return a
}

// sync makes sure Discord shows act (nil = nothing), (re)connecting as needed.
func (p *Presence) sync(appID string, act *discord.Activity) {
	want := ""
	if act != nil {
		b, _ := json.Marshal(act)
		want = string(b)
	}

	p.mu.Lock()
	c := p.client
	if c != nil {
		select {
		case <-c.Done():
			log.Printf("Discord connection lost")
			c, p.client, p.sent = nil, nil, ""
		default:
		}
	}
	if c != nil && p.appID != appID {
		log.Printf("Discord application ID changed, reconnecting")
		p.client = nil
		p.mu.Unlock()
		c.SetActivity(nil)
		c.Close()
		p.mu.Lock()
		c, p.sent = nil, ""
	}
	p.mu.Unlock()

	if appID == "" {
		p.setErr("Set your Discord Application ID in Settings.")
		return
	}
	if c == nil {
		nc, err := discord.Dial(appID)
		if err != nil {
			p.setErr("Not connected to Discord: " + err.Error())
			return
		}
		log.Printf("connected to Discord")
		p.mu.Lock()
		p.client, p.appID, p.sent = nc, appID, ""
		p.mu.Unlock()
		c = nc
		p.setErr("")
	}

	p.mu.Lock()
	sent := p.sent
	p.mu.Unlock()
	if want == sent {
		return
	}
	if err := c.SetActivity(act); err != nil {
		log.Printf("SET_ACTIVITY failed: %v", err)
		p.setErr("Discord refused the update: " + err.Error())
		return
	}
	if act != nil {
		log.Printf("presence set: %s", act.Details)
	} else {
		log.Printf("presence cleared")
	}
	p.mu.Lock()
	p.sent = want
	p.mu.Unlock()
	p.setErr("")
}

func (p *Presence) setErr(e string) {
	p.mu.Lock()
	if p.lastErr != e && e != "" {
		log.Print(e)
	}
	p.lastErr = e
	p.mu.Unlock()
}
