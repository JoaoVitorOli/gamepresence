package main

import (
	"golang.org/x/sys/windows"
)

// MyGo's single-instance lock uses a Unix socket in %TEMP%, which fails
// silently when the user name has non-ASCII characters. A named mutex and
// event work regardless of paths.
const (
	instanceMutex = `Local\GamePresence-7d3f2a1c-lock`
	instanceEvent = `Local\GamePresence-7d3f2a1c-show`
)

// acquireSingleInstance returns true in the first instance, which then gets
// onShow called whenever another instance starts. A later instance asks the
// first one to show its window and gets false.
func acquireSingleInstance(onShow func()) bool {
	name, _ := windows.UTF16PtrFromString(instanceMutex)
	evName, _ := windows.UTF16PtrFromString(instanceEvent)
	_, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		if ev, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, evName); err == nil {
			windows.SetEvent(ev)
			windows.CloseHandle(ev)
		}
		return false
	}
	// The mutex handle is kept open for the life of the process.
	ev, err := windows.CreateEvent(nil, 0, 0, evName)
	if err != nil {
		return true
	}
	go func() {
		for {
			if r, _ := windows.WaitForSingleObject(ev, windows.INFINITE); r != windows.WAIT_OBJECT_0 {
				return
			}
			onShow()
		}
	}()
	return true
}
