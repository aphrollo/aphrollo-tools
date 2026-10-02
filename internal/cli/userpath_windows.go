//go:build windows

package cli

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type registryUserPath struct{}

func defaultUserPathStore() userPathStore { return registryUserPath{} }

func (registryUserPath) Read() (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	v, typ, err := k.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, typ == registry.EXPAND_SZ, nil
}

func (registryUserPath) Write(raw string, expand bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if expand {
		err = k.SetExpandStringValue("Path", raw)
	} else {
		err = k.SetStringValue("Path", raw)
	}
	if err != nil {
		return err
	}
	broadcastEnvironmentChange()
	return nil
}

// broadcastEnvironmentChange sends WM_SETTINGCHANGE("Environment") so new
// shells and Explorer pick the value up without a logoff. A failed broadcast
// is not an error: the registry write already happened.
func broadcastEnvironmentChange() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	env, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	var result uintptr
	_, _, _ = proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
}
