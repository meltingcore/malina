// Package desktop exposes the small Wails service used by Malina's native UI.
// It owns background-job lifecycle, cooperative pause and cancellation, and
// exclusive restore-target reservations. Backup and restore mechanics remain in
// package core so the desktop and CLI share the same safety checks.
package desktop
