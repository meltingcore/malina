package core

import "time"

const (
	// BackupFormat identifies Malina backup manifests.
	BackupFormat = "malina-backup"
	// BackupFormatVersion is incremented when the on-disk manifest becomes incompatible.
	BackupFormatVersion = 1
	// ImageFilename is the only image name accepted inside a Malina backup directory.
	ImageFilename = "disk.img.gz"
)

// Connection contains ephemeral SSH connection settings. Password fields must never be persisted.
type Connection struct {
	Host           string `json:"host"`
	Identity       string `json:"identity,omitempty"`
	Password       string `json:"password,omitempty"`
	SudoPassword   string `json:"sudoPassword,omitempty"`
	Port           int    `json:"port,omitempty"`
	UseDefaultKeys bool   `json:"useDefaultKeys,omitempty"`
}

// Progress describes one point-in-time update from a long-running operation.
type Progress struct {
	Phase       string  `json:"phase"`
	Message     string  `json:"message"`
	Bytes       int64   `json:"bytes,omitempty"`
	TotalBytes  int64   `json:"totalBytes,omitempty"`
	Fraction    float64 `json:"fraction,omitempty"`
	Source      string  `json:"source,omitempty"`
	Destination string  `json:"destination,omitempty"`
}

// ProgressFunc receives operation progress. Implementations must return quickly.
type ProgressFunc func(Progress)

// Job is the serialisable state of a desktop background operation.
type Job struct {
	ID          string     `json:"id"`
	Type        string     `json:"type"`
	Title       string     `json:"title"`
	Subtitle    string     `json:"subtitle"`
	Status      string     `json:"status"`
	Phase       string     `json:"phase"`
	Message     string     `json:"message"`
	Bytes       int64      `json:"bytes,omitempty"`
	TotalBytes  int64      `json:"totalBytes,omitempty"`
	Fraction    float64    `json:"fraction,omitempty"`
	Source      string     `json:"source,omitempty"`
	Destination string     `json:"destination,omitempty"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Error       string     `json:"error,omitempty"`
	ResultPath  string     `json:"resultPath,omitempty"`
}

// PiInfo describes the source disk and access capabilities discovered over SSH.
type PiInfo struct {
	Hostname           string   `json:"hostname"`
	Model              string   `json:"model"`
	OS                 string   `json:"os"`
	Architecture       string   `json:"architecture"`
	RootSource         string   `json:"rootSource"`
	RootDisk           string   `json:"rootDisk"`
	RootFilesystem     string   `json:"rootFilesystem"`
	BootSource         string   `json:"bootSource,omitempty"`
	BootDisk           string   `json:"bootDisk,omitempty"`
	DiskSize           int64    `json:"diskSize"`
	LogicalSectorSize  int64    `json:"logicalSectorSize,omitempty"`
	SudoAvailable      bool     `json:"sudoAvailable"`
	DirectDiskAccess   bool     `json:"directDiskAccess"`
	PasswordlessSudo   bool     `json:"passwordlessSudo"`
	SudoPasswordNeeded bool     `json:"sudoPasswordNeeded"`
	Supported          bool     `json:"supported"`
	Warnings           []string `json:"warnings"`
}

// Manifest is the durable metadata required to validate and restore a backup.
type Manifest struct {
	Format        string         `json:"format"`
	FormatVersion int            `json:"formatVersion"`
	Status        string         `json:"status"`
	Consistency   string         `json:"consistency"`
	CreatedAt     time.Time      `json:"createdAt"`
	CompletedAt   time.Time      `json:"completedAt"`
	Source        ManifestSource `json:"source"`
	Image         ManifestImage  `json:"image"`
	Warning       string         `json:"warning"`
}

// ManifestSource records the system and whole-disk source used for a backup.
type ManifestSource struct {
	Host              string `json:"host"`
	Hostname          string `json:"hostname"`
	Model             string `json:"model"`
	OS                string `json:"os"`
	Architecture      string `json:"architecture"`
	Device            string `json:"device"`
	Bytes             int64  `json:"bytes"`
	LogicalSectorSize int64  `json:"logicalSectorSize,omitempty"`
	RootFilesystem    string `json:"rootFilesystem"`
}

// ManifestImage records the compressed and uncompressed image integrity metadata.
type ManifestImage struct {
	File             string `json:"file"`
	Compression      string `json:"compression"`
	RawBytes         int64  `json:"rawBytes"`
	CompressedBytes  int64  `json:"compressedBytes"`
	SHA256           string `json:"sha256"`
	CompressedSHA256 string `json:"compressedSha256"`
}

// Backup combines an absolute backup directory with its validated manifest.
type Backup struct {
	Path     string   `json:"path"`
	Manifest Manifest `json:"manifest"`
}

// VerifyResult reports successful verification and the checksums that were observed.
type VerifyResult struct {
	Valid            bool     `json:"valid"`
	RawBytes         int64    `json:"rawBytes"`
	SHA256           string   `json:"sha256"`
	CompressedSHA256 string   `json:"compressedSha256"`
	Manifest         Manifest `json:"manifest"`
}

// Device describes a whole physical restore destination.
type Device struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Bytes     int64  `json:"bytes"`
	Transport string `json:"transport"`
	Removable bool   `json:"removable"`
	// Stable is true when ID comes from hardware/media identity rather than a reusable OS path.
	Stable bool `json:"stable"`
}

// RestoreResult describes a completed restore and optional read-back verification.
type RestoreResult struct {
	Device       string   `json:"device"`
	BytesWritten int64    `json:"bytesWritten"`
	Verified     bool     `json:"verified"`
	Ejected      bool     `json:"ejected"`
	Manifest     Manifest `json:"manifest"`
}

// BackupRequest contains all inputs needed to create a backup.
type BackupRequest struct {
	Connection      Connection `json:"connection"`
	OutputDirectory string     `json:"outputDirectory"`
}

// RestoreRequest contains the selected backup, target identifier, and erase confirmation.
type RestoreRequest struct {
	BackupPath string `json:"backupPath"`
	Device     string `json:"device"`
	Confirm    string `json:"confirmation"`
	Verify     bool   `json:"verifyWrite"`
}
