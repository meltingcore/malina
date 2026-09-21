// Package core implements Malina's platform-independent backup, verification,
// restore, SSH, and removable-device safety rules.
//
// The package treats restore destinations as hostile, reusable identifiers:
// callers must confirm a device selector, the selector must resolve through
// DeviceOperations, and the same physical device must still be present after
// unmounting before it is opened for writing.
package core
