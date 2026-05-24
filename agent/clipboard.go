// Clipboard interface — platform-specific implementations
package main

// GetClipboard returns current clipboard text content ("" if none/unavailable)
// SetClipboard sets the clipboard text content
type ClipboardManager interface {
	GetText() string
	SetText(text string) error
}
