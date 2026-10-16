//go:build windows

package notify

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pSHQueryUserNotificationState = windows.NewLazySystemDLL("shell32.dll").NewProc("SHQueryUserNotificationState")
	pNtQueryWnfStateData          = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryWnfStateData")
)

// wnfQuietHoursProfile is WNF_SHEL_QUIETHOURS_ACTIVE_PROFILE_CHANGED: the Focus assist / Do not
// disturb state (0 = off, 1 = priority only, 2 = alarms only). Not documented by Microsoft, but
// stable since Windows 10 1803 and widely used; any failure reads as "not quiet".
const wnfQuietHoursProfile uint64 = 0x0D83063EA3BF1C75

// Quiet is true while Windows asks apps to keep quiet: Do not disturb / Focus assist on, quiet
// hours, or presentation mode. Full-screen games alone do not count (that is when people play).
func Quiet() bool {
	if quietHours() {
		return true
	}
	var state int32
	if r, _, _ := pSHQueryUserNotificationState.Call(uintptr(unsafe.Pointer(&state))); int32(r) >= 0 {
		const presentationMode, quietTime = 4, 6
		if state == presentationMode || state == quietTime {
			return true
		}
	}
	return false
}

func quietHours() bool {
	if pNtQueryWnfStateData.Find() != nil {
		return false
	}
	name := wnfQuietHoursProfile
	var stamp uint32
	var profile uint32
	size := uint32(unsafe.Sizeof(profile))
	r, _, _ := pNtQueryWnfStateData.Call(uintptr(unsafe.Pointer(&name)), 0, 0,
		uintptr(unsafe.Pointer(&stamp)), uintptr(unsafe.Pointer(&profile)), uintptr(unsafe.Pointer(&size)))
	return r == 0 && size >= 4 && profile != 0
}
