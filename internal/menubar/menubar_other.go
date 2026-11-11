//go:build !darwin

package menubar

func Available() bool                        { return false }
func Init(url string, show bool)             {}
func Run()                                   { select {} }
func SetVisible(show bool)                   {}
func SetIcon(style string, level, count int) {}
func ClosePopover()                          {}
func SetHeight(h int)                        {}
