//go:build darwin

package menubar

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa -framework WebKit -framework CoreGraphics
#include <stdlib.h>
#include "menubar.h"
*/
import "C"

import (
	"os"
	"strconv"
	"unsafe"
)

// Available reports whether this process can show things on a Mac's screen (a signed-in user's session; not over SSH).
func Available() bool {
	return os.Getenv("HOMEOWL_NO_MENUBAR") == "" && C.hoGUIAvailable() == 1
}

// Init prepares the menubar item; it must run on the main thread, before Run.
func Init(url string, show bool) {
	cu := C.CString(url)
	defer C.free(unsafe.Pointer(cu))
	sh := 0
	if show {
		sh = 1
	}
	C.hoInit(cu, C.int(sh))
}

// Run runs the macOS event loop on the main thread; it never returns.
func Run() { C.hoRun() }

func SetVisible(show bool) {
	if show {
		C.hoSetVisible(1)
	} else {
		C.hoSetVisible(0)
	}
}

// SetIcon shows style; level 0 = all fine, 1 = something to look at, 2 = internet or power down; count = problems.
func SetIcon(style string, level, count int) {
	png, tmpl, dot := icon(style)
	title := ""
	if style == "count" && count > 0 {
		title = " " + strconv.Itoa(count)
	}
	ct := C.CString(title)
	defer C.free(unsafe.Pointer(ct))
	C.hoSetIcon(unsafe.Pointer(&png[0]), C.int(len(png)), b2i(tmpl), b2i(dot), C.int(level), ct)
}

func ClosePopover()   { C.hoClosePopover() }
func SetHeight(h int) { C.hoSetHeight(C.int(h)) }

func b2i(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
