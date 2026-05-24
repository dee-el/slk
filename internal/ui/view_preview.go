// internal/ui/view_preview.go
//
// Image-preview overlay panel renderer for App.View (Phase 6i).
//
// When the full-screen image preview is active, the messages
// region and thread region are skipped (see renderMessagesRegion
// and the thread visibility gate). The preview panel takes their
// place in the panels list: a single overlay spanning the
// combined (msgWidth + msgBorder + threadWidth + threadBorder)
// when both messages and thread are visible, or just the
// messages width when the thread is hidden.
//
// The rail and sidebar still render normally above the preview
// so the user can see context (which workspace is active, which
// channel the preview was opened from).
//
// Caller is responsible for the visibility gate; this helper
// assumes preview is active.
package ui

// renderPreviewPanel returns the exact-sized preview overlay
// panel string. Width is the combined messages+thread region.
func (a *App) renderPreviewPanel(frame panelLayoutFrame) string {
	overlayW := frame.MsgWidth + frame.MsgBorder
	if a.threadVisible && frame.ThreadWidth > 0 {
		overlayW += frame.ThreadWidth + frame.ThreadBorder
	}
	overlayContent := a.preview.Overlay().View(overlayW, frame.ContentHeight, a.imgProtocol)
	return exactSize(overlayContent, overlayW, frame.ContentHeight)
}
