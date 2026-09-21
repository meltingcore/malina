package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/meltingcore/malina/internal/core"
)

const (
	jobStatusRunning    = "running"
	jobStatusPaused     = "paused"
	jobStatusCancelling = "cancelling"
	jobStatusCompleted  = "completed"
	jobStatusCancelled  = "cancelled"
	jobStatusFailed     = "failed"
)

type managedJob struct {
	job       core.Job
	cancel    context.CancelFunc
	control   *core.StreamControl
	cancelled bool
	target    string
}

func jobIsActive(status string) bool {
	return status == jobStatusRunning || status == jobStatusPaused || status == jobStatusCancelling
}

func (s *Service) emitJob(job core.Job) {
	if s.app != nil {
		s.app.Event.Emit("malina:job", job)
	}
}

func (s *Service) createJob(jobType, title, subtitle, source, destination, target string) (*managedJob, context.Context, core.Job) {
	s.jobsMu.Lock()
	s.jobSequence++
	prefix := "BKP"
	if jobType == "restore" {
		prefix = "RST"
	}
	id := fmt.Sprintf("%s-%04d", prefix, s.jobSequence)
	ctx, cancel := context.WithCancel(s.appCtx)
	control := core.NewStreamControl()
	job := core.Job{
		ID:          id,
		Type:        jobType,
		Title:       title,
		Subtitle:    subtitle,
		Status:      jobStatusRunning,
		Phase:       "queued",
		Message:     "Starting job",
		Source:      source,
		Destination: destination,
		StartedAt:   time.Now().UTC(),
	}
	managed := &managedJob{job: job, cancel: cancel, control: control, target: target}
	s.jobs[id] = managed
	s.jobsMu.Unlock()
	s.emitJob(job)
	return managed, core.WithStreamControl(ctx, control), job
}

func (s *Service) updateJob(id string, update func(*core.Job)) bool {
	s.jobsMu.Lock()
	managed := s.jobs[id]
	if managed == nil {
		s.jobsMu.Unlock()
		return false
	}
	update(&managed.job)
	snapshot := managed.job
	s.jobsMu.Unlock()
	s.emitJob(snapshot)
	return true
}

func (s *Service) jobProgress(id string) core.ProgressFunc {
	return func(progress core.Progress) {
		s.updateJob(id, func(job *core.Job) {
			job.Phase = progress.Phase
			job.Message = progress.Message
			if progress.Bytes > 0 || progress.Phase == "backup" || progress.Phase == "restore" || progress.Phase == "readback" {
				job.Bytes = progress.Bytes
			}
			if progress.TotalBytes > 0 {
				job.TotalBytes = progress.TotalBytes
			}
			if progress.Fraction > 0 || progress.Phase == "complete" {
				job.Fraction = progress.Fraction
			} else if job.TotalBytes > 0 {
				job.Fraction = float64(job.Bytes) / float64(job.TotalBytes)
			}
			if progress.Source != "" {
				job.Source = progress.Source
			}
			if progress.Destination != "" {
				job.Destination = progress.Destination
			}
		})
	}
}

func (s *Service) finishJob(id string, err error, resultPath string) {
	s.jobsMu.RLock()
	managed := s.jobs[id]
	cancelled := managed != nil && managed.cancelled
	s.jobsMu.RUnlock()
	s.updateJob(id, func(job *core.Job) {
		now := time.Now().UTC()
		job.CompletedAt = &now
		job.ResultPath = resultPath
		if err == nil {
			job.Status = jobStatusCompleted
			job.Phase = "complete"
			job.Fraction = 1
			job.Message = "Job complete"
			return
		}
		if cancelled {
			job.Status = jobStatusCancelled
			job.Phase = "cancelled"
			job.Message = "Job cancelled"
			return
		}
		job.Status = jobStatusFailed
		job.Phase = "failed"
		job.Message = "Job failed"
		job.Error = err.Error()
	})
}

func (s *Service) StartBackup(request core.BackupRequest) (core.Job, error) {
	if request.Connection.Host == "" {
		return core.Job{}, core.NewError("HOST_REQUIRED", "Enter the Pi's SSH address.")
	}
	if request.OutputDirectory == "" {
		return core.Job{}, core.NewError("OUTPUT_REQUIRED", "Choose a directory in which to store the backup.")
	}
	title := "Remote SD Card Backup — " + request.Connection.Host
	managed, ctx, job := s.createJob("backup", title, "Detecting source disk", request.Connection.Host, request.OutputDirectory, "")
	go func() {
		defer managed.cancel()
		result, err := s.engine.Backup(ctx, request, s.jobProgress(job.ID))
		resultPath := ""
		if err == nil {
			resultPath = result.Path
		}
		s.finishJob(job.ID, err, resultPath)
	}()
	return job, nil
}

func (s *Service) StartRestore(request core.RestoreRequest) (core.Job, error) {
	if request.BackupPath == "" {
		return core.Job{}, core.NewError("BACKUP_REQUIRED", "Choose a backup first.")
	}
	if request.Device == "" || request.Confirm != request.Device {
		return core.Job{}, core.NewError("CONFIRMATION_REQUIRED", "Type the exact destination device to confirm erasure.")
	}

	s.jobsMu.RLock()
	appCtx := s.appCtx
	s.jobsMu.RUnlock()
	available, err := s.engine.Devices.List(appCtx)
	if err != nil {
		return core.Job{}, err
	}
	var target *core.Device
	for index := range available {
		if available[index].ID == request.Device || available[index].Path == request.Device {
			target = &available[index]
			break
		}
	}
	if target == nil {
		return core.Job{}, core.NewError("UNSAFE_DESTINATION", "The destination is no longer available. Refresh devices and try again.")
	}

	// Normalise the request to the best available identity before reserving it.
	// This makes concurrent path-based and stable-ID requests contend on one key.
	request.Device = target.ID
	request.Confirm = target.ID
	s.jobsMu.Lock()
	if _, busy := s.restoreTargets[target.ID]; busy {
		s.jobsMu.Unlock()
		return core.Job{}, core.NewError("DEVICE_BUSY", "Another restore job is already using "+target.Path+".")
	}
	s.restoreTargets[target.ID] = struct{}{}
	s.jobsMu.Unlock()

	backupName := filepath.Base(request.BackupPath)
	title := "Restore Image — " + target.Path
	subtitle := backupName + " → " + target.Path
	managed, ctx, job := s.createJob("restore", title, subtitle, request.BackupPath, target.Path, target.ID)
	go func() {
		defer managed.cancel()
		defer func() {
			s.jobsMu.Lock()
			delete(s.restoreTargets, target.ID)
			s.jobsMu.Unlock()
		}()
		_, err := s.engine.Restore(ctx, request, s.jobProgress(job.ID))
		s.finishJob(job.ID, err, target.Path)
	}()
	return job, nil
}

func (s *Service) ListJobs() []core.Job {
	s.jobsMu.RLock()
	result := make([]core.Job, 0, len(s.jobs))
	for _, managed := range s.jobs {
		result = append(result, managed.job)
	}
	s.jobsMu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].StartedAt.After(result[j].StartedAt) })
	return result
}

func (s *Service) PauseJob(id string) bool {
	s.jobsMu.Lock()
	managed := s.jobs[id]
	if managed == nil || managed.job.Status != jobStatusRunning || !managed.control.Pause() {
		s.jobsMu.Unlock()
		return false
	}
	managed.job.Status = jobStatusPaused
	managed.job.Phase = "paused"
	managed.job.Message = "Job paused"
	snapshot := managed.job
	s.jobsMu.Unlock()
	s.emitJob(snapshot)
	return true
}

func (s *Service) ResumeJob(id string) bool {
	s.jobsMu.Lock()
	managed := s.jobs[id]
	if managed == nil || managed.job.Status != jobStatusPaused || !managed.control.Resume() {
		s.jobsMu.Unlock()
		return false
	}
	managed.job.Status = jobStatusRunning
	managed.job.Phase = "resumed"
	managed.job.Message = "Job resumed"
	snapshot := managed.job
	s.jobsMu.Unlock()
	s.emitJob(snapshot)
	return true
}

func (s *Service) CancelJob(id string) bool {
	s.jobsMu.Lock()
	managed := s.jobs[id]
	if managed == nil || !jobIsActive(managed.job.Status) || managed.cancelled {
		s.jobsMu.Unlock()
		return false
	}
	managed.cancelled = true
	managed.job.Status = jobStatusCancelling
	managed.job.Phase = "cancelling"
	managed.job.Message = "Stopping job"
	snapshot := managed.job
	cancel := managed.cancel
	s.jobsMu.Unlock()
	s.emitJob(snapshot)
	cancel()
	return true
}
