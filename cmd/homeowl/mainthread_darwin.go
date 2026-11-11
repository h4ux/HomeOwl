package main

import "runtime"

// macOS only draws windows and menu bar items from the main thread; keep main() on it.
func init() { runtime.LockOSThread() }
