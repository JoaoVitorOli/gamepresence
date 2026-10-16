# Game Presence

Discord Rich Presence for any Windows game: pick the process, the name and the cover art.

Discord only detects games it knows. Game Presence watches the processes you register and shows the game on your Discord profile with the name and cover you choose. That covers PC ports, emulated games, and anything else Discord doesn't recognize. It talks to the Discord desktop app over local IPC, so no bot or login is needed.

Built in Go with [MyGo](https://github.com/egoist/mygo) (v0.5.5). The UI is native, drawn by MyGo with Direct3D 11, with no WebView2 and no HTML.

## Features

- **Automatic detection**: checks running processes every 5 seconds, sets the activity when a registered game starts and clears it when the game closes. If Discord is opened later, the app reconnects to it.
- **Emulator-friendly**: match by exe name, optionally narrowed by text in the window title or the command line, so several games that share one exe can each have their own entry.
- **Cover art from three sources**: search IGDB, paste an image URL, or upload a local file to Imgur or catbox.moe.
- **Lightweight**: a single process that lives in the tray, with start-with-Windows and single-instance support.
- **Private by design**: API keys are stored in Windows Credential Manager, never in the config file or the log.

## Install

Download or build `Game Presence.exe`. It is a single executable and needs no installation. `go tool mygo build` also produces an optional installer, `Game Presence Setup <version> amd64.exe`.

Closing the window keeps the app running in the tray. Click the tray icon to open the window, or right-click it for **Pause presence** and **Quit**. Launching the exe again only brings back the window of the copy that is already running.

### Resource use (measured)

- **In the tray**, which is where the app spends most of its time: about 3 MB of memory in use, and near-zero CPU.
- **With the window open**: about 70–80 MB, still one process. An earlier WebView2 version used about 370 MB across 7 processes.
- Closing the window destroys it and returns its memory to Windows.

## Setup

### 1. Discord Application ID (required)

1. Open the [Discord Developer Portal](https://discord.com/developers/applications) and click **New Application**.
2. Give it a generic name such as "a game". Discord shows the application's name as the title of the activity card.
3. Under **General Information**, copy the **Application ID** and paste it into **Settings → Application ID** in the app.
4. In Discord, open **User Settings → Activity Privacy** and turn on **Share your detected activities with others**.

### 2. IGDB cover search (optional)

1. Enable two-factor authentication on Twitch, which Twitch requires for developer apps. Then open the [Twitch developer console](https://dev.twitch.tv/console/apps) and click **Register Your Application**.
2. Use any name, OAuth Redirect URL `http://localhost`, category **Application Integration** and client type **Confidential**.
3. Copy the **Client ID** and generate a **Client Secret** with **New Secret**. Paste both under **Settings → IGDB cover search**.

### 3. Uploading local images (optional)

Discord can only show images from a public link, so local files are uploaded to an image host first. **Uploaded images are public**: anyone with the link can open them.

- **Imgur**: register an app at [api.imgur.com/oauth2/addclient](https://api.imgur.com/oauth2/addclient), choosing "Anonymous usage without user authorization", and paste its **Client ID**. Images can be deleted later.
- **catbox.moe**: no account is needed, but uploaded images cannot be deleted.

## Adding a game

1. Start the game.
2. Click **Add game → Choose running…** and pick its process.
3. Enter the **Game name**. For the cover, use **Search IGDB**, **Paste URL** or **Local file**.
4. Click **Save**. Discord shows the game within 5 seconds. **Test** shows a game on Discord even when it isn't running.

For emulators that run many games through the same exe, add each game with that exe and a filter under **Only when**: **window title contains** or **command line contains**, for example the game's folder or ID.

Example: the native Bloodborne port runs as `bb-probe.exe` with the window title "Bloodborne™", so registering `bb-probe.exe` with the name "Bloodborne" and its IGDB cover is all it takes.

## What Discord shows

- **Profile and activity card**: the game's name (the `details` field), the cover, the optional status line and the elapsed time.
- **Member list**: with **Show the game's name in the member list** on, the app sends `status_display_type = 2` (Details), so the list reads "Playing Bloodborne" instead of the application's name.
- **Activity card title**: always the application's name. It cannot be changed per game over IPC, which is why a generic application name works best.
- Covers must be public `https://` URLs of at most 256 characters.

## Files

- Settings and games: `%APPDATA%\Game Presence\config.json`
- Log: `%APPDATA%\Game Presence\app.log`

## Building

Requires Go 1.27 or later. Keep the project in a path with only ASCII characters.

```
go tool mygo build
```

The executable and the installer are written to `dist\windows-amd64\`.

## Tests

```
go test ./...
```

The UI tests run headless on a temporary config, never yours. `internal/discord` also has a live test that sets a real activity for a few seconds; it runs only when `DISCORD_APP_ID` is set.
