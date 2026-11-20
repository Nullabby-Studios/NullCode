//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"strings"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var sendMessageTimeoutW = user32.NewProc("SendMessageTimeoutW")

const (
	wmSettingChange = 0x001A
	smtoAbortIfHung = 0x0002
)

// addToPath prepends dir to the user PATH (registry) and notifies running
// programs so new terminals pick it up.
func addToPath(dir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	old, _, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	for _, p := range strings.Split(old, ";") {
		if strings.EqualFold(strings.TrimSpace(p), dir) {
			broadcast()
			return nil
		}
	}
	joined := dir
	if old != "" {
		joined = dir + ";" + old
	}
	if err := key.SetExpandStringValue("Path", joined); err != nil {
		return err
	}
	broadcast()
	return nil
}

// removeFromPath drops dir from the user PATH.
func removeFromPath(dir string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	old, _, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	var keep []string
	for _, p := range strings.Split(old, ";") {
		if !strings.EqualFold(strings.TrimSpace(p), dir) {
			keep = append(keep, p)
		}
	}
	if len(keep) == len(strings.Split(old, ";")) {
		return nil
	}
	if err := key.SetExpandStringValue("Path", strings.Join(keep, ";")); err != nil {
		return err
	}
	broadcast()
	return nil
}

// broadcast tells the shell the environment changed (WM_SETTINGCHANGE).
func broadcast() {
	const hwndBroadcast = 0xFFFF
	sendMessageTimeoutW.Call(hwndBroadcast, wmSettingChange, 0, 0, smtoAbortIfHung, 2000, 0)
}
