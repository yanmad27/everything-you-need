package jobscheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// SimpleJob represents a basic scheduled job
type SimpleJob struct {
	Name      string
	Interval  time.Duration
	DailyTime string // Format: "15:04" for daily scheduling (optional)
	Handler   func() error
	enabled   bool
	lastRun   time.Time
}

// SimpleScheduler is a basic job scheduler
type SimpleScheduler struct {
	jobs    []*SimpleJob
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
}

// NewSimpleScheduler creates a new simple scheduler
func NewSimpleScheduler() *SimpleScheduler {
	return &SimpleScheduler{
		jobs: make([]*SimpleJob, 0),
	}
}

// RegisterJob registers a new job with interval
func (s *SimpleScheduler) RegisterJob(name string, interval time.Duration, handler func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	job := &SimpleJob{
		Name:     name,
		Interval: interval,
		Handler:  handler,
		enabled:  true,
		lastRun:  time.Time{}, // Never run
	}

	s.jobs = append(s.jobs, job)
	log.Printf("Registered job: %s (interval: %v)", name, interval)
}

// RegisterDailyJob registers a new job that runs daily at a specific time
func (s *SimpleScheduler) RegisterDailyJob(name string, timeStr string, handler func() error) error {
	// Validate time format
	if _, err := time.Parse("15:04", timeStr); err != nil {
		return fmt.Errorf("invalid time format %s, expected HH:MM", timeStr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job := &SimpleJob{
		Name:      name,
		DailyTime: timeStr,
		Handler:   handler,
		enabled:   true,
		lastRun:   time.Time{}, // Never run
	}

	s.jobs = append(s.jobs, job)
	log.Printf("Registered daily job: %s (time: %s)", name, timeStr)
	return nil
}

// Start begins the scheduler
func (s *SimpleScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true
	s.mu.Unlock()

	log.Printf("Starting simple job scheduler with %d jobs", len(s.jobs))

	// Start monitoring goroutine
	go s.run()
}

// Stop gracefully stops the scheduler
func (s *SimpleScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	log.Println("Stopping job scheduler...")
	s.cancel()
	s.running = false
	log.Println("Job scheduler stopped")
}

// EnableJob enables a specific job
func (s *SimpleScheduler) EnableJob(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, job := range s.jobs {
		if job.Name == name {
			job.enabled = true
			log.Printf("Enabled job: %s", name)
			return
		}
	}
}

// DisableJob disables a specific job
func (s *SimpleScheduler) DisableJob(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, job := range s.jobs {
		if job.Name == name {
			job.enabled = false
			log.Printf("Disabled job: %s", name)
			return
		}
	}
}

// GetJobs returns all registered jobs info
func (s *SimpleScheduler) GetJobs() []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]map[string]interface{}, len(s.jobs))
	for i, job := range s.jobs {
		jobInfo := map[string]interface{}{
			"name":     job.Name,
			"enabled":  job.enabled,
			"last_run": job.lastRun,
		}

		if job.DailyTime != "" {
			jobInfo["daily_time"] = job.DailyTime
			jobInfo["type"] = "daily"
		} else {
			jobInfo["interval"] = job.Interval.String()
			jobInfo["type"] = "interval"
		}

		result[i] = jobInfo
	}

	return result
}

// run is the main scheduler loop
func (s *SimpleScheduler) run() {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkAndRunJobs()
		}
	}
}

// checkAndRunJobs checks which jobs need to run
func (s *SimpleScheduler) checkAndRunJobs() {
	s.mu.RLock()
	now := time.Now()
	jobsToRun := make([]*SimpleJob, 0)

	for _, job := range s.jobs {
		if !job.enabled {
			continue
		}

		shouldRun := false

		if job.DailyTime != "" {
			// Daily job - check if it's time to run today
			shouldRun = s.shouldRunDailyJob(job, now)
		} else if job.Interval > 0 {
			// Interval job - check if enough time has passed
			if job.lastRun.IsZero() || now.Sub(job.lastRun) >= job.Interval {
				shouldRun = true
			}
		}

		if shouldRun {
			jobsToRun = append(jobsToRun, job)
		}
	}
	s.mu.RUnlock()

	// Execute jobs
	for _, job := range jobsToRun {
		go s.executeJob(job)
	}
}

// shouldRunDailyJob checks if a daily job should run
func (s *SimpleScheduler) shouldRunDailyJob(job *SimpleJob, now time.Time) bool {
	// Parse the target time
	_, err := time.Parse("15:04", job.DailyTime)
	if err != nil {
		log.Printf("Invalid time format for job %s: %s", job.Name, job.DailyTime)
		return false
	}

	// Create today's target datetime
	today := now.Format("2006-01-02")
	todayTarget, err := time.Parse("2006-01-02 15:04", today+" "+job.DailyTime)
	if err != nil {
		return false
	}

	// Check if we've already run today
	if !job.lastRun.IsZero() {
		lastRunDate := job.lastRun.Format("2006-01-02")
		todayDate := now.Format("2006-01-02")
		if lastRunDate == todayDate {
			// Already ran today
			return false
		}
	}

	// Check if current time is past the target time
	return now.After(todayTarget)
}

// executeJob runs a single job
func (s *SimpleScheduler) executeJob(job *SimpleJob) {
	log.Printf("Running job: %s", job.Name)
	start := time.Now()

	// Update last run time
	s.mu.Lock()
	job.lastRun = start
	s.mu.Unlock()

	// Execute job handler
	if err := job.Handler(); err != nil {
		log.Printf("Job %s failed: %v", job.Name, err)
	} else {
		duration := time.Since(start)
		log.Printf("Job %s completed in %v", job.Name, duration)
	}
}
