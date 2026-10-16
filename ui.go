package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gamepresence/internal/procs"
)

// view is the state of one open window. It is made when the window opens
// and dropped when it closes, so nothing of the UI stays in memory while
// the app sits in the tray. It is only touched on the main thread; other
// goroutines go through update.
type view struct {
	b      *Backend
	ask    func(mygo.MessageOptions) int // shows a question, returns the button
	win    atomic.Pointer[mygo.Window]
	images map[string]*imageEntry
	icon   *ui.Bitmap

	tab       int
	status    Status
	games     []Game
	settings  Settings
	autostart bool

	// Settings form.
	sAppID, sTwitchID, sTwitchSecret, sImgur, sHost string
	sShowInStatus, sElapsed                         bool
	settingsMsg                                     string

	// Game editor.
	editing     bool
	draft       Game
	imgTab      int
	igdbQuery   string
	igdbResults []IGDBResult
	igdbMsg     string
	urlInput    string
	matchMode   string
	pickedPath  string
	pickedBmp   *ui.Bitmap
	uploading   bool
	editorErr   string

	// Process picker.
	picking      bool
	procList     []procs.Process
	procFilter   string
	windowedOnly bool
	procState    ui.ListState
}

// askNative shows a native message box. Cancel is the default button, so
// Enter or Escape never confirm a destructive choice; -1 on error.
func askNative(o mygo.MessageOptions) int {
	res, err := mygo.Dialog.Message(o)
	if err != nil {
		return -1
	}
	return res.Button
}

type imageEntry struct {
	bmp     *ui.Bitmap
	loading bool
	failed  bool
}

var (
	matchModes  = []string{"any instance of this exe", "window title contains", "command line contains"}
	modeValues  = []string{"", "title", "cmdline"}
	uploadHosts = []string{"Imgur (needs your Client ID, can be deleted later)", "catbox.moe (no account, cannot be deleted)"}
	hostValues  = []string{"imgur", "catbox"}
)

func newView(b *Backend, icon []byte, autostart bool) *view {
	v := &view{b: b, images: map[string]*imageEntry{}, windowedOnly: true, autostart: autostart, ask: askNative}
	v.icon, _ = ui.DecodeBitmap(icon)
	v.status = b.presence.Status()
	v.games = b.store.Games()
	v.loadSettings(b.store.Settings())
	if v.settings.DiscordAppID == "" {
		v.tab = 2
	}
	return v
}

// update runs fn on the main thread and redraws. The first frame is built
// while the window is being made, before win is set: then fn just runs on
// the main thread, and the frame that follows shows it.
func (v *view) update(fn func()) {
	if w := v.win.Load(); w != nil {
		w.Update(fn)
		return
	}
	mygo.RunOnMain(func() {
		fn()
		if w := v.win.Load(); w != nil {
			w.Invalidate()
		}
	})
}

func (v *view) loadSettings(s Settings) {
	v.settings = s
	v.sAppID, v.sTwitchID = s.DiscordAppID, s.TwitchClientID
	v.sTwitchSecret, v.sImgur = "", ""
	v.sShowInStatus, v.sElapsed = s.ShowGameInStatus, s.ShowElapsed
	v.sHost = uploadHosts[0]
	if s.UploadHost == "catbox" {
		v.sHost = uploadHosts[1]
	}
}

func pick(labels, values []string, label string) string {
	for i, l := range labels {
		if l == label {
			return values[i]
		}
	}
	return values[0]
}

func labelOf(labels, values []string, value string) string {
	for i, v := range values {
		if v == value {
			return labels[i]
		}
	}
	return labels[0]
}

// image returns the bitmap for a URL, fetching it in the background the
// first time (nil until it arrives).
func (v *view) image(url string) *ui.Bitmap {
	if url == "" {
		return nil
	}
	e := v.images[url]
	if e == nil {
		e = &imageEntry{loading: true}
		v.images[url] = e
		go func() {
			data, err := fetchImage(url)
			v.update(func() {
				e.loading = false
				if err == nil {
					e.bmp, err = ui.DecodeBitmap(data)
				}
				e.failed = err != nil
			})
		}()
	}
	return e.bmp
}

func fetchImage(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxUpload))
}

// cover draws an image box, or an empty placeholder of the same size.
func (v *view) cover(c *ui.Context, bmp *ui.Bitmap, w, h float32) {
	t := c.Theme()
	if bmp == nil {
		ui.Box(c).Size(w, h).Radius(6).Background(t.Surface)
		return
	}
	ui.Image(c, bmp).Size(w, h).Fit(ui.Cover).Radius(6)
}

func (v *view) render(c *ui.Context) {
	t := *c.Theme()
	t.Accent = ui.Hex("#5865f2")
	t.AccentHover = ui.Hex("#4752c4")
	t.AccentPressed = ui.Hex("#3c45a5")
	t.AccentText = ui.Hex("#ffffff")
	c.SetTheme(&t)

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		v.topbar(c)
		ui.Scroll(c).Grow(1).Padding(16).Gap(12).Children(func() {
			v.nowPlaying(c)
			switch v.tab {
			case 0:
				v.gamesPage(c)
			case 1:
				v.settingsPage(c)
			default:
				v.helpPage(c)
			}
		})
	})
	ui.Modal(c, &v.editing, func() { v.editor(c) }).Width(900)
	ui.Modal(c, &v.picking, func() { v.picker(c) }).Width(820)
}

func (v *view) topbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(16).Padding(10, 16).Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			if v.icon != nil {
				ui.Image(c, v.icon).Size(24, 24)
			}
			ui.Text(c, appName).Bold()
		})
		ui.Tabs(c, &v.tab, "Games", "Settings", "Setup guide")
		ui.Spacer(c)
		color, text := t.Danger, "Discord not connected"
		if v.status.DiscordConnected {
			color, text = t.Success, "Discord connected"
			if v.status.DiscordUser != "" {
				text += " as " + v.status.DiscordUser
			}
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Box(c).Size(9, 9).Radius(5).Background(color)
			ui.Text(c, text).TextColor(t.TextMuted).FontSize(13)
		})
	})
}

func elapsed(startMs int64) string {
	if startMs == 0 {
		return ""
	}
	d := time.Since(time.UnixMilli(startMs)).Truncate(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d elapsed", h, m, s)
	}
	return fmt.Sprintf("%d:%02d elapsed", m, s)
}

func (v *view) nowPlaying(c *ui.Context) {
	t := c.Theme()
	s := v.status
	playing := s.GameName != ""
	label, name := "NOT PLAYING", "No registered game is running"
	switch {
	case s.Testing:
		label = "TESTING ON DISCORD"
	case s.Paused:
		label, name = "PAUSED", "Presence is paused"
	case playing:
		label = "NOW SHOWING ON DISCORD"
	}
	if playing {
		name = s.GameName
	}
	ui.Row(c).Gap(14).Padding(12).Radius(10).Background(t.Surface).Border(1, t.Border).Children(func() {
		v.cover(c, v.image(s.ImageURL), 64, 86)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			lc := t.TextMuted
			if playing {
				lc = t.Success
			}
			ui.Text(c, label).FontSize(11).Bold().TextColor(lc)
			ui.Text(c, name).FontSize(17).Bold().SingleLine()
			if playing {
				var meta []string
				if s.PID != 0 {
					meta = append(meta, fmt.Sprintf("PID %d", s.PID))
				}
				if v.settings.ShowElapsed && s.StartMs > 0 {
					meta = append(meta, elapsed(s.StartMs))
					c.After(time.Second)
				}
				ui.Text(c, strings.Join(meta, " · ")).TextColor(t.TextMuted).FontSize(13)
			}
		})
		if s.Testing && ui.Button(c, "Stop test").Clicked() {
			v.b.StopTest()
		}
		paused := s.Paused
		ui.Row(c).Gap(8).Children(func() {
			if ui.Switch(c, &paused).Label("Pause").Changed() {
				v.b.SetPaused(paused)
			}
			ui.Text(c, "Pause").TextColor(t.TextMuted)
		})
	})
	if s.DiscordError != "" {
		banner(c, s.DiscordError, t.Danger)
	}
}

func banner(c *ui.Context, text string, color ui.Color) {
	ui.Box(c).Padding(9, 12).Radius(8).Background(color.Alpha(0.15)).Border(1, color.Alpha(0.5)).Children(func() {
		ui.Text(c, text).FontSize(13).Selectable()
	})
}

func (v *view) refreshGames() { v.games = v.b.store.Games() }

func (v *view) gamesPage(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Children(func() {
		ui.Text(c, "Your games").FontSize(15).Bold().Grow(1)
		if ui.PrimaryButton(c, "+ Add game").Clicked() {
			v.openEditor(nil)
		}
	})
	ui.Text(c, "When several are running, the one highest in the list wins. Processes are checked every 5 seconds.").
		FontSize(12).TextColor(t.TextMuted)
	if len(v.games) == 0 {
		ui.Box(c).Padding(30).Radius(10).Border(1, t.Border).BorderStyle(ui.BorderDashed).Children(func() {
			ui.Text(c, "No games yet. Start your game, then click Add game and pick its process.").
				TextColor(t.TextMuted).TextAlign(ui.Center)
		})
		return
	}
	for i, g := range v.games {
		v.gameRow(c.Key(g.ID), i, g)
	}
}

func matchSummary(g Game) string {
	if g.MatchText == "" || g.MatchMode == "" {
		return g.Exe
	}
	what := "command line"
	if g.MatchMode == "title" {
		what = "title"
	}
	return fmt.Sprintf("%s · %s contains “%s”", g.Exe, what, g.MatchText)
}

func (v *view) gameRow(c *ui.Context, i int, g Game) {
	t := c.Theme()
	running := v.status.GameID == g.ID
	border := t.Border
	if running {
		border = t.Success
	}
	row := ui.Row(c).Gap(12).Padding(8, 10).Radius(10).Background(t.Surface).Border(1, border)
	if !g.Enabled {
		row.Opacity(0.55)
	}
	row.Children(func() {
		v.cover(c, v.image(g.ImageURL), 42, 56)
		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, g.Name).Bold().SingleLine()
				if running {
					word := "running"
					if v.status.Testing {
						word = "testing"
					}
					ui.Badge(c, word).Background(t.Success).TextColor(ui.Hex("#ffffff"))
				}
			})
			ui.Text(c, matchSummary(g)).FontSize(12).TextColor(t.TextMuted).SingleLine()
			if g.State != "" {
				ui.Text(c, g.State).FontSize(12).TextColor(t.TextMuted).SingleLine()
			}
		})
		enabled := g.Enabled
		if ui.Switch(c, &enabled).Label("Enabled").Tooltip("Enabled").Changed() {
			v.b.SetGameEnabled(g.ID, enabled)
			v.refreshGames()
		}
		ui.Button(c, "▲").Tooltip("Higher priority").Disabled(i == 0).OnClick(func() {
			v.b.MoveGame(g.ID, -1)
			v.refreshGames()
		})
		ui.Button(c, "▼").Tooltip("Lower priority").Disabled(i == len(v.games)-1).OnClick(func() {
			v.b.MoveGame(g.ID, 1)
			v.refreshGames()
		})
		ui.Button(c, "Test").Tooltip("Show it on Discord now, even if it isn't running").OnClick(func() {
			v.b.TestGame(g.ID)
		})
		ui.Button(c, "Edit").OnClick(func() { v.openEditor(&g) })
		ui.Button(c, "Delete").TextColor(t.Danger).OnClick(func() { v.confirmDelete(g) })
	})
}

func (v *view) confirmDelete(g Game) {
	go func() {
		if v.ask(mygo.MessageOptions{
			Parent:        v.win.Load(),
			Type:          mygo.MessageQuestion,
			Message:       fmt.Sprintf("Remove “%s” from your games?", g.Name),
			Buttons:       []string{"Remove", "Cancel"},
			DefaultButton: 1,
			CancelButton:  1,
		}) != 0 {
			return
		}
		v.b.DeleteGame(g.ID)
		v.update(v.refreshGames)
	}()
}

// ---------- settings ----------

func hint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(12).TextColor(c.Theme().TextMuted)
}

func section(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(15).Bold().Margin(10, 0, 0, 0)
}

func (v *view) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).MaxWidth(640).Gap(8).Children(func() {
		section(c, "Discord")
		ui.Field(c, "Application ID", func() {
			ui.TextInput(c, &v.sAppID).Placeholder("e.g. 1234567890123456789")
		})
		ui.Row(c).Gap(4).Wrap().Children(func() {
			hint(c, "Create one application at")
			ui.Link(c, "discord.com/developers/applications", "https://discord.com/developers/applications").FontSize(12)
			hint(c, "and paste its Application ID. Its name is what Discord shows after “Playing”, so call it something generic like “a game”.")
		})
		ui.Checkbox(c, &v.sShowInStatus, "Show the game's name in the member list (instead of the application's name)")
		ui.Checkbox(c, &v.sElapsed, "Show elapsed time")

		section(c, "App")
		if ui.Checkbox(c, &v.autostart, "Start with Windows (hidden in the tray)").Changed() {
			if err := v.b.SetAutostart(v.autostart); err != nil {
				v.autostart = !v.autostart
				c.Toast(err.Error())
			}
		}

		section(c, "IGDB cover search")
		ui.Row(c).Gap(4).Wrap().Children(func() {
			hint(c, "Uses a Twitch developer app. Create one at")
			ui.Link(c, "dev.twitch.tv/console/apps", "https://dev.twitch.tv/console/apps").FontSize(12)
			hint(c, "(category “Application Integration”, OAuth redirect http://localhost).")
		})
		ui.Field(c, "Twitch Client ID", func() { ui.TextInput(c, &v.sTwitchID) })
		ui.Field(c, "Twitch Client Secret", func() {
			ui.Row(c).Gap(6).Children(func() {
				ph := "not set"
				if v.settings.TwitchSecretSet {
					ph = "saved (type to replace)"
				}
				ui.TextInput(c, &v.sTwitchSecret).Password().Placeholder(ph).Grow(1)
				if v.settings.TwitchSecretSet && ui.Button(c, "Clear").Clicked() {
					v.saveSettings(c, "-", "")
				}
			})
		})

		section(c, "Uploading local images")
		ui.Text(c, "Discord can only show images from a public link, so local files are uploaded first. Uploaded images are public: anyone with the link can open them.").
			FontSize(12).TextColor(t.Warning)
		ui.Field(c, "Upload to", func() { ui.Select(c, &v.sHost, uploadHosts) })
		if pick(uploadHosts, hostValues, v.sHost) == "imgur" {
			ui.Field(c, "Imgur Client ID", func() {
				ui.Row(c).Gap(6).Children(func() {
					ph := "not set"
					if v.settings.ImgurClientIDSet {
						ph = "saved (type to replace)"
					}
					ui.TextInput(c, &v.sImgur).Password().Placeholder(ph).Grow(1)
					if v.settings.ImgurClientIDSet && ui.Button(c, "Clear").Clicked() {
						v.saveSettings(c, "", "-")
					}
				})
			})
			ui.Row(c).Gap(4).Wrap().Children(func() {
				hint(c, "Register an app at")
				ui.Link(c, "api.imgur.com/oauth2/addclient", "https://api.imgur.com/oauth2/addclient").FontSize(12)
				hint(c, "(“Anonymous usage without user authorization”).")
			})
		}

		ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
			if ui.PrimaryButton(c, "Save settings").Clicked() {
				v.saveSettings(c, v.sTwitchSecret, v.sImgur)
			}
			if ui.Button(c, "Open config folder").Clicked() {
				v.b.OpenConfigFolder()
			}
		})
		hint(c, "Secrets are stored in Windows Credential Manager, not in the config file.")
	})
}

func (v *view) saveSettings(c *ui.Context, twitchSecret, imgur string) {
	s, err := v.b.SaveSettings(Settings{
		DiscordAppID:       v.sAppID,
		ShowGameInStatus:   v.sShowInStatus,
		ShowElapsed:        v.sElapsed,
		TwitchClientID:     v.sTwitchID,
		TwitchClientSecret: twitchSecret,
		ImgurClientID:      imgur,
		UploadHost:         pick(uploadHosts, hostValues, v.sHost),
	})
	if err != nil {
		c.Toast(err.Error())
		return
	}
	v.loadSettings(s)
	c.Toast("Settings saved")
}

// ---------- help ----------

func (v *view) helpPage(c *ui.Context) {
	t := c.Theme()
	item := func(n, text string) {
		ui.Row(c).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Text(c, n).TextColor(t.TextMuted)
			ui.Text(c, text).Grow(1)
		})
	}
	ui.Column(c).MaxWidth(680).Gap(8).Children(func() {
		section(c, "Getting started")
		item("1.", "Open the Discord Developer Portal, click New Application, name it (for example “a game”) and copy the Application ID from General Information.")
		ui.Link(c, "Open the Discord Developer Portal", "https://discord.com/developers/applications").MarginStart(20)
		item("2.", "Paste it in Settings → Application ID and save.")
		item("3.", "In Discord: User Settings → Activity Privacy, turn on “Share your detected activities with others”.")
		item("4.", "Start your game, click Add game, pick its process, choose a name and a cover.")
		item("5.", "Optional: add Twitch keys to search covers on IGDB, or an Imgur Client ID to upload your own images.")

		section(c, "What Discord shows")
		item("•", "Profile and activity card: the game's name, the cover, your status line and the elapsed time.")
		item("•", "Member list: with “Show the game's name in the member list” on, it reads “Playing <game>”. Otherwise it shows the application's name.")
		item("•", "The title of the activity card is always the application's name: that is how Discord works for every app that uses one Application ID.")

		section(c, "Emulators")
		ui.Text(c, "When several games run through the same exe, add each game with the same exe and a filter on the window title or the command line (for example the game's folder or ID).")
	})
}

// ---------- game editor ----------

func (v *view) openEditor(g *Game) {
	if g != nil {
		v.draft = *g
	} else {
		v.draft = Game{Enabled: true, ImageSource: "igdb"}
	}
	v.matchMode = labelOf(matchModes, modeValues, v.draft.MatchMode)
	v.urlInput = ""
	if v.draft.ImageSource == "url" {
		v.urlInput = v.draft.ImageURL
	}
	v.imgTab = map[string]int{"url": 1, "upload": 2}[v.draft.ImageSource]
	v.igdbQuery, v.igdbResults, v.igdbMsg = v.draft.Name, nil, ""
	if v.settings.TwitchClientID == "" || !v.settings.TwitchSecretSet {
		v.igdbMsg = "Add your Twitch Client ID and Secret in Settings to search IGDB."
	}
	v.pickedPath, v.pickedBmp, v.uploading, v.editorErr = "", nil, false, ""
	v.editing = true
}

func (v *view) editor(c *ui.Context) {
	t := c.Theme()
	title := "Add game"
	if v.draft.ID != "" {
		title = "Edit game"
	}
	ui.Text(c, title).FontSize(17).Bold()
	ui.Grid(c).Columns(2).GapX(22).GapY(8).Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Field(c, "Process (exe)", func() {
				ui.Row(c).Gap(6).Children(func() {
					ui.TextInput(c, &v.draft.Exe).Placeholder("bb-probe.exe").Grow(1)
					if ui.Button(c, "Choose running…").Clicked() {
						v.openPicker()
					}
				})
			})
			ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
				ui.Field(c, "Only when", func() { ui.Select(c, &v.matchMode, matchModes) }).Grow(1)
				ui.Field(c, "Text", func() {
					ui.TextInput(c, &v.draft.MatchText).Placeholder("(for emulators)").
						Disabled(pick(matchModes, modeValues, v.matchMode) == "")
				}).Grow(1)
			})
			ui.Field(c, "Game name", func() { ui.TextInput(c, &v.draft.Name).Placeholder("Bloodborne") })
			ui.Field(c, "Status line (optional)", func() {
				ui.TextInput(c, &v.draft.State).Placeholder("e.g. Hunting in Yharnam")
			})
			ui.Checkbox(c, &v.draft.Enabled, "Enabled")
		})
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "COVER IMAGE").FontSize(11).Bold().TextColor(t.TextMuted)
			ui.Segmented(c, &v.imgTab, "Search IGDB", "Paste URL", "Local file")
			switch v.imgTab {
			case 0:
				v.igdbPane(c)
			case 1:
				if ui.TextInput(c, &v.urlInput).Placeholder("https://…/cover.jpg").Changed() {
					v.draft.ImageURL = strings.TrimSpace(v.urlInput)
					v.draft.ImageSource, v.draft.IGDBID, v.draft.DeleteHash = "url", 0, ""
				}
				hint(c, "Any public https image link. Discord fetches it through its own proxy.")
			case 2:
				v.uploadPane(c)
			}
			v.preview(c)
		})
	})
	if v.editorErr != "" {
		banner(c, v.editorErr, t.Danger)
	}
	ui.Row(c).Gap(10).Justify(ui.End).Margin(8, 0, 0, 0).Children(func() {
		if ui.Button(c, "Cancel").Clicked() {
			v.editing = false
		}
		if ui.PrimaryButton(c, "Save").Clicked() {
			v.saveGame()
		}
	})
}

func (v *view) saveGame() {
	if v.pickedPath != "" {
		v.editorErr = "Upload the chosen file first, or pick another image source."
		return
	}
	v.draft.MatchMode = pick(matchModes, modeValues, v.matchMode)
	if _, err := v.b.SaveGame(v.draft); err != nil {
		v.editorErr = err.Error()
		return
	}
	v.editing = false
	v.refreshGames()
}

func (v *view) igdbPane(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(6).Children(func() {
		in := ui.TextInput(c, &v.igdbQuery).Placeholder("Search a game…").Grow(1)
		if ui.Button(c, "Search").Clicked() || in.Submitted() {
			v.searchIGDB()
		}
	})
	if v.igdbMsg != "" {
		hint(c, v.igdbMsg)
	}
	if len(v.igdbResults) == 0 {
		return
	}
	ui.Scroll(c).MaxHeight(260).Children(func() {
		ui.Grid(c).Columns(4).Gap(8).Children(func() {
			for _, r := range v.igdbResults {
				r := r
				btn := ui.ButtonBase(c.Key(r.ID)).Padding(4).Radius(6).Background(t.Surface).Disabled(r.CoverURL == "")
				if r.CoverURL != "" && r.CoverURL == v.draft.ImageURL {
					btn.Border(2, t.Accent)
				} else if btn.Hovered() {
					btn.Background(t.SurfaceHover)
				}
				btn.Children(func() {
					ui.Column(c).Gap(3).FillWidth().Children(func() {
						if bmp := v.image(r.CoverURL); bmp != nil {
							ui.Image(c, bmp).FillWidth().AspectRatio(3.0 / 4).Fit(ui.Cover).Radius(4)
						} else {
							ui.Box(c).FillWidth().AspectRatio(3.0 / 4).Radius(4).Background(t.Background)
						}
						ui.Text(c, r.Name).FontSize(11).MaxLines(2)
						y := ""
						if r.Year > 0 {
							y = fmt.Sprint(r.Year)
						}
						if r.CoverURL == "" {
							y += " · no cover"
						}
						ui.Text(c, y).FontSize(10).TextColor(t.TextMuted)
					})
				})
				if btn.Clicked() {
					v.draft.ImageURL, v.draft.ImageSource, v.draft.IGDBID, v.draft.DeleteHash = r.CoverURL, "igdb", r.ID, ""
					if strings.TrimSpace(v.draft.Name) == "" {
						v.draft.Name = r.Name
					}
				}
			}
		})
	})
}

func (v *view) searchIGDB() {
	q := strings.TrimSpace(v.igdbQuery)
	if q == "" {
		return
	}
	v.igdbMsg, v.igdbResults = "Searching…", nil
	go func() {
		res, err := v.b.SearchIGDB(q)
		v.update(func() {
			v.igdbResults, v.igdbMsg = res, ""
			switch {
			case err != nil:
				v.igdbMsg = err.Error()
			case len(res) == 0:
				v.igdbMsg = "No results."
			}
		})
	}()
}

func (v *view) uploadPane(c *ui.Context) {
	t := c.Theme()
	host := "Imgur"
	if v.settings.UploadHost == "catbox" {
		host = "catbox.moe"
	}
	banner(c, "The file will be uploaded to "+host+" and become public: anyone with the link can see it.", t.Warning)
	ui.Row(c).Gap(10).Children(func() {
		if ui.Button(c, "Choose file…").Disabled(v.uploading).Clicked() {
			v.chooseFile()
		}
		label := "Upload and use"
		if v.uploading {
			label = "Uploading…"
		}
		if ui.PrimaryButton(c, label).Disabled(v.pickedPath == "" || v.uploading).Clicked() {
			v.confirmUpload(host)
		}
	})
	if v.pickedPath != "" {
		hint(c, v.pickedPath[strings.LastIndexAny(v.pickedPath, `\/`)+1:])
	}
}

func (v *view) chooseFile() {
	go func() {
		path, data, err := v.b.PickImageFile(v.win.Load())
		if err != nil || path == "" {
			if err != nil {
				v.update(func() { v.editorErr = err.Error() })
			}
			return
		}
		v.update(func() {
			bmp, err := ui.DecodeBitmap(data)
			if err != nil {
				v.editorErr = "Could not read that image: " + err.Error()
				return
			}
			v.pickedPath, v.pickedBmp, v.editorErr = path, bmp, ""
		})
	}()
}

func (v *view) confirmUpload(host string) {
	path := v.pickedPath
	go func() {
		if v.ask(mygo.MessageOptions{
			Parent:        v.win.Load(),
			Type:          mygo.MessageWarning,
			Message:       "Upload this image to " + host + "?",
			Detail:        "The image will be public: anyone with the link can see it.",
			Buttons:       []string{"Upload", "Cancel"},
			DefaultButton: 1,
			CancelButton:  1,
		}) != 0 {
			return
		}
		v.update(func() { v.uploading = true })
		r, err := v.b.UploadImage(path)
		v.update(func() {
			v.uploading = false
			if err != nil {
				v.editorErr = err.Error()
				return
			}
			v.draft.ImageURL, v.draft.DeleteHash, v.draft.ImageSource, v.draft.IGDBID = r.URL, r.DeleteHash, "upload", 0
			v.pickedPath, v.pickedBmp, v.editorErr = "", nil, ""
		})
	}()
}

func (v *view) preview(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(12).Padding(10).Radius(8).Background(t.Background).Margin(4, 0, 0, 0).AlignItems(ui.Start).Children(func() {
		bmp, note := v.pickedBmp, "Not uploaded yet"
		if bmp == nil {
			bmp, note = v.image(v.draft.ImageURL), v.draft.ImageURL
			if e := v.images[v.draft.ImageURL]; e != nil && e.failed {
				note = "Could not load this image: " + note
			}
		}
		if bmp == nil {
			ui.Box(c).Size(90, 90).Radius(8).Background(t.Surface).Center().Children(func() {
				ui.Text(c, "No image").FontSize(12).TextColor(t.TextMuted)
			})
		} else {
			ui.Image(c, bmp).Size(90, 90).Fit(ui.Cover).Radius(8)
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, "PREVIEW").FontSize(11).Bold().TextColor(t.TextMuted)
			name := v.draft.Name
			if name == "" {
				name = "Game name"
			}
			ui.Text(c, name).Bold()
			if v.draft.State != "" {
				ui.Text(c, v.draft.State).FontSize(12).TextColor(t.TextMuted)
			}
			if note != "" {
				ui.Text(c, note).FontSize(11).TextColor(t.TextMuted).MaxLines(2)
			}
			if v.draft.ImageURL != "" && ui.Button(c, "Remove image").Clicked() {
				v.draft.ImageURL, v.draft.IGDBID, v.draft.DeleteHash, v.urlInput = "", 0, "", ""
			}
		})
	})
}

// ---------- process picker ----------

func (v *view) openPicker() {
	v.picking, v.procFilter, v.procList = true, "", nil
	v.loadProcs()
}

func (v *view) loadProcs() {
	go func() {
		list, _ := v.b.ListProcesses()
		v.update(func() { v.procList = list })
	}()
}

func (v *view) picker(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Children(func() {
		ui.Text(c, "Running processes").FontSize(17).Bold().Grow(1)
		if ui.Button(c, "Close").Clicked() {
			v.picking = false
		}
	})
	ui.Row(c).Gap(10).Children(func() {
		ui.TextInput(c, &v.procFilter).Placeholder("Filter by name, title or command line").AutoFocus().Grow(1)
		ui.Checkbox(c, &v.windowedOnly, "With a window only")
		if ui.Button(c, "Refresh").Clicked() {
			v.loadProcs()
		}
	})
	f := strings.ToLower(strings.TrimSpace(v.procFilter))
	var shown []procs.Process
	for _, p := range v.procList {
		if v.windowedOnly && p.Title == "" {
			continue
		}
		if f != "" && !strings.Contains(strings.ToLower(p.Exe+" "+p.Title+" "+p.CommandLine), f) {
			continue
		}
		shown = append(shown, p)
	}
	ui.List(c, &v.procState, len(shown), func(i int) {
		p := shown[i]
		row := ui.ButtonBase(c.Key(p.PID)).Padding(7, 10).BorderWidth(0, 0, 1, 0).BorderColor(t.Border)
		if row.Hovered() {
			row.Background(t.SurfaceHover)
		}
		row.Children(func() {
			ui.Column(c).Grow(1).Gap(1).Children(func() {
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, p.Exe).Bold().SingleLine().Width(190)
					title := p.Title
					if title == "" {
						title = "(no window)"
					}
					ui.Text(c, title).SingleLine().Grow(1)
				})
				cmd := p.CommandLine
				if cmd == "" {
					cmd = p.Path
				}
				ui.Text(c, cmd).FontSize(11).TextColor(t.TextMuted).SingleLine().Font("Consolas")
			})
		})
		if row.Clicked() {
			v.draft.Exe = p.Exe
			if strings.TrimSpace(v.draft.Name) == "" && p.Title != "" {
				v.draft.Name = p.Title
				if strings.TrimSpace(v.igdbQuery) == "" {
					v.igdbQuery = p.Title
				}
			}
			v.picking = false
		}
	}).Height(440).Border(1, t.Border).Radius(8).Children(func() {
		if len(shown) == 0 {
			msg := "No matching processes."
			if v.procList == nil {
				msg = "Loading…"
			}
			ui.Text(c, msg).Padding(10).TextColor(t.TextMuted)
		}
	})
}
