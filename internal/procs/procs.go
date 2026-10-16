//go:build windows

// Package procs lists running processes with their window titles and
// command lines using plain Win32 / NT calls.
package procs

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type Process struct {
	PID         uint32 `json:"pid"`
	Exe         string `json:"exe"`  // e.g. bb-probe.exe
	Path        string `json:"path"` // full image path, may be empty
	Title       string `json:"title"`
	CommandLine string `json:"commandLine"`
	StartMs     int64  `json:"startMs"` // creation time, unix ms
}

var (
	user32                        = windows.NewLazySystemDLL("user32.dll")
	ntdll                         = windows.NewLazySystemDLL("ntdll.dll")
	procEnumWindows               = user32.NewProc("EnumWindows")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	procNtQueryInformationProcess = ntdll.NewProc("NtQueryInformationProcess")
)

// Snapshot returns all processes (exe name and PID only, cheap).
func Snapshot() ([]Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Process
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == 0 || e.ProcessID == 4 {
			continue
		}
		out = append(out, Process{PID: e.ProcessID, Exe: windows.UTF16ToString(e.ExeFile[:])})
	}
	return out, nil
}

// WindowTitles maps PID -> title of its first visible, titled top-level window.
func WindowTitles() map[uint32]string {
	titles := map[uint32]string{}
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		n, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if n == 0 {
			return 1
		}
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if _, ok := titles[pid]; ok {
			return 1
		}
		buf := make([]uint16, n+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
		titles[pid] = windows.UTF16ToString(buf)
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return titles
}

// Fill populates Path, CommandLine and StartMs (best effort; protected
// processes just keep empty fields).
func Fill(p *Process) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.PID)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		p.Path = windows.UTF16ToString(buf[:n])
	}
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(h, &c, &e, &k, &u) == nil {
		p.StartMs = c.Nanoseconds() / 1e6
	}
	p.CommandLine = commandLine(h)
}

// commandLine uses ProcessCommandLineInformation (60), available since
// Windows 8.1, which only needs PROCESS_QUERY_LIMITED_INFORMATION.
func commandLine(h windows.Handle) string {
	const processCommandLineInformation = 60
	size := uint32(1024)
	for tries := 0; tries < 3; tries++ {
		buf := make([]byte, size)
		var ret uint32
		st, _, _ := procNtQueryInformationProcess.Call(uintptr(h), processCommandLineInformation,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(size), uintptr(unsafe.Pointer(&ret)))
		if st == 0 {
			us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
			if us.Buffer == nil || us.Length == 0 {
				return ""
			}
			return windows.UTF16ToString(unsafe.Slice(us.Buffer, us.Length/2))
		}
		if ret <= size {
			return ""
		}
		size = ret
	}
	return ""
}

// List returns processes that are useful to show in a picker: everything
// with a visible window first, then the rest, with details filled.
func List() ([]Process, error) {
	all, err := Snapshot()
	if err != nil {
		return nil, err
	}
	titles := WindowTitles()
	for i := range all {
		all[i].Title = titles[all[i].PID]
		Fill(&all[i])
	}
	return all, nil
}

// SameExe compares exe names case-insensitively, accepting either a bare
// name or a full path on either side.
func SameExe(a, b string) bool {
	return strings.EqualFold(filepath.Base(a), filepath.Base(b))
}
