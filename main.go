package main

import (
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
)

const appName = "Game Presence"

//go:embed assets/tray.png
var trayIcon []byte

//go:embed assets/icon.png
var appIcon []byte

func setupLog() {
	dir := configDir()
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "app.log")
	if st, err := os.Stat(path); err == nil && st.Size() > 1<<20 {
		os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
}

func main() {
	mygo.App.SetName(appName)
	showReq := make(chan struct{}, 1)
	if !acquireSingleInstance(func() {
		select {
		case showReq <- struct{}{}:
		default:
		}
	}) {
		return // the running instance shows its window instead
	}
	setupLog()

	store, err := LoadStore()
	if err != nil {
		log.Printf("could not read config, starting fresh: %v", err)
	}

	var (
		win  atomic.Pointer[mygo.Window]
		cur  atomic.Pointer[view]
		tray atomic.Pointer[mygo.Tray]
	)
	backend := NewBackend(store, func(s Status) {
		if t := tray.Load(); t != nil {
			t.SetToolTip(trayText(s))
		}
		if v := cur.Load(); v != nil {
			v.update(func() { v.status = s })
		}
	})

	// The window, drawn natively by MyGo (no webview), only exists while it
	// is open: closing it destroys it and its state, so the app idles in the
	// tray with no UI in memory.
	showWindow := func() {
		if w := win.Load(); w != nil {
			w.Show()
			w.Restore()
			w.Focus()
			return
		}
		v := newView(backend, appIcon, mygo.App.OpenAtLogin())
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:     appName,
			Width:     1000,
			Height:    720,
			MinWidth:  780,
			MinHeight: 540,
			StateKey:  "main",
			Content:   ui.View(v.render),
		})
		v.win.Store(w)
		// StateKey restores the last size and place, but never open minimized.
		w.Restore()
		w.Focus()
		w.OnClosed(func() {
			win.CompareAndSwap(w, nil)
			cur.CompareAndSwap(v, nil)
			go trimMemory()
		})
		win.Store(w)
		cur.Store(v)
	}

	// Closing the window leaves the app running in the tray.
	mygo.App.OnWindowAllClosed(func() {})

	stop := make(chan struct{})
	mygo.App.WhenReady(func() {
		if !mygo.App.WasOpenedAtLogin() {
			showWindow()
		}

		menu := func() *mygo.Menu {
			return mygo.NewMenu([]*mygo.MenuItem{
				{Label: "Open Game Presence", Click: func(*mygo.MenuItem, *mygo.Window) { showWindow() }},
				mygo.Separator(),
				{Label: "Pause presence", Type: mygo.MenuItemCheckbox, Checked: backend.presence.Status().Paused,
					Click: func(it *mygo.MenuItem, _ *mygo.Window) { backend.SetPaused(it.Checked) }},
				mygo.Separator(),
				{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
			})
		}
		t, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon, ToolTip: appName})
		if err != nil {
			log.Printf("tray: %v", err)
		} else {
			t.OnClick(showWindow)
			t.OnRightClick(func() {
				t.SetMenu(menu())
				t.PopUpMenu()
				t.SetMenu(nil)
			})
			tray.Store(t)
		}

		go backend.presence.Run(stop)
		go func() {
			for range showReq {
				mygo.RunOnMain(showWindow)
			}
		}()
	})

	err = mygo.App.Run()
	close(stop)
	backend.presence.Shutdown()
	log.Printf("exiting")
	if err != nil {
		log.Fatal(err)
	}
}

// trimMemory gives memory the closed window used back to Windows, so the
// app is small while it sits in the tray.
func trimMemory() {
	time.Sleep(2 * time.Second)
	debug.FreeOSMemory()
	windows.SetProcessWorkingSetSizeEx(windows.CurrentProcess(), ^uintptr(0), ^uintptr(0), 0)
}

func trayText(s Status) string {
	text := "not playing"
	switch {
	case s.Paused && !s.Testing:
		text = "paused"
	case s.GameName != "":
		text = "playing " + s.GameName
	}
	if !s.DiscordConnected {
		text += " (Discord not connected)"
	}
	return appName + ": " + text
}
