package userdir

import "os"

// Owned reports whether the current user owns the file. Windows gives every
// user a temporary directory of their own and Riffle's directories live
// beneath it, so ownership is not checked there.
func Owned(os.FileInfo) bool { return true }
