package jobscheduler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CronJob struct {
	Name     string
	CronExpr string
	Handler  func() error
	enabled  bool
	lastRun  time.Time
}

type JobScheduler struct {
	jobs    []*CronJob
	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
}

func NewJobScheduler() *JobScheduler {
	return &JobScheduler{
		jobs: make([]*CronJob, 0),
	}
}

func (s *JobScheduler) RegisterCronJob(name string, cronExpr string, handler func() error) error {
	if cronExpr == "" {
		return fmt.Errorf("cron expression cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job := &CronJob{
		Name:     name,
		CronExpr: cronExpr,
		Handler:  handler,
		enabled:  true,
		lastRun:  time.Time{},
	}

	s.jobs = append(s.jobs, job)
	log.Printf("Registered cron job: %s (cron: %s)", name, cronExpr)
	return nil
}

func (s *JobScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}

	s.ctx, s.cancel = context.WithCancel(ctx)
	s.running = true
	s.mu.Unlock()

	log.Printf("Starting simple job scheduler with %d jobs", len(s.jobs))

	go s.run()
}

func (s *JobScheduler) Stop() {
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

func (s *JobScheduler) EnableJob(name string) {
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

func (s *JobScheduler) DisableJob(name string) {
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

func (s *JobScheduler) GetJobs() []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]map[string]any, len(s.jobs))
	for i, job := range s.jobs {
		jobInfo := map[string]any{
			"name":     job.Name,
			"enabled":  job.enabled,
			"last_run": job.lastRun,
		}

		jobInfo["cron_expr"] = job.CronExpr
		jobInfo["type"] = "cron"

		result[i] = jobInfo
	}

	return result
}

func (s *JobScheduler) run() {
	ticker := time.NewTicker(10 * time.Second)
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

func (s *JobScheduler) checkAndRunJobs() {
	s.mu.RLock()
	now := time.Now().In(time.FixedZone("UTC+7", 7*60*60))
	jobsToRun := make([]*CronJob, 0)

	for _, job := range s.jobs {
		if !job.enabled {
			continue
		}

		if s.shouldRunCronJob(job, now) {
			jobsToRun = append(jobsToRun, job)
		}
	}
	s.mu.RUnlock()

	for _, job := range jobsToRun {
		go s.executeJob(job)
	}
}

func (s *JobScheduler) shouldRunCronJob(job *CronJob, now time.Time) bool {
	if !isValidCron(job.CronExpr) {
		return false
	}

	if job.lastRun.IsZero() {
		return true
	}

	return isCronMatch(job.CronExpr, now)
}

func (s *JobScheduler) executeJob(job *CronJob) {
	log.Printf("Running job: %s", job.Name)
	start := time.Now()

	s.mu.Lock()
	job.lastRun = start
	s.mu.Unlock()

	if err := job.Handler(); err != nil {
		log.Printf("Job %s failed: %v", job.Name, err)
	} else {
		duration := time.Since(start)
		log.Printf("Job %s completed in %v", job.Name, duration)
	}
}

func isCronMatch(cronExpression string, now ...time.Time) bool {
	var currentTime time.Time
	if len(now) > 0 {
		currentTime = now[0]
	} else {
		currentTime = time.Now()
	}

	if !isValidCron(cronExpression) {
		return false
	}

	parts := strings.Fields(strings.TrimSpace(cronExpression))

	minute := currentTime.Minute()
	hour := currentTime.Hour()
	day := currentTime.Day()
	month := int(currentTime.Month())
	weekday := int(currentTime.Weekday())

	return matchesField(parts[0], minute) &&
		matchesField(parts[1], hour) &&
		matchesField(parts[2], day) &&
		matchesField(parts[3], month) &&
		matchesField(parts[4], weekday)
}

func matchesField(field string, current int) bool {
	if field == "*" {
		return true
	}

	if strings.Contains(field, "/") {
		parts := strings.Split(field, "/")
		step, _ := strconv.Atoi(parts[1])
		if parts[0] == "*" {
			return current%step == 0
		}
		return matchesField(parts[0], current) && current%step == 0
	}

	if strings.Contains(field, ",") {
		for _, part := range strings.Split(field, ",") {
			if matchesField(strings.TrimSpace(part), current) {
				return true
			}
		}
		return false
	}

	if strings.Contains(field, "-") {
		parts := strings.Split(field, "-")
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		return current >= start && current <= end
	}

	value, _ := strconv.Atoi(field)
	return value == current
}

func isValidCron(cronExpression string) bool {
	parts := strings.Fields(strings.TrimSpace(cronExpression))
	return len(parts) == 5
}
