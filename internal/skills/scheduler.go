package skills

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-co-op/gocron/v2"

	"github.com/justinpbarnett/virgil/internal"
	"github.com/justinpbarnett/virgil/internal/agent"
)

// Scheduler runs skills on their configured cron schedules.
type Scheduler struct {
	s gocron.Scheduler
}

// NewScheduler creates a scheduler for all skills that have a Schedule field.
// push is called with the skill output after each successful run; pass nil to skip push.
func NewScheduler(ag *agent.Agent, loaded []*internal.Skill, push func(string)) (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("create scheduler: %w", err)
	}

	for _, sk := range loaded {
		if sk.Schedule == "" || !sk.Enabled {
			continue
		}
		sk := sk // capture for closure
		_, err := s.NewJob(
			gocron.CronJob(sk.Schedule, false),
			gocron.NewTask(func() {
				ctx := context.Background()
				text, err := ag.RunSkill(ctx, sk, "cron")
				if err != nil {
					slog.Error("scheduled skill failed", "skill", sk.Name, "err", err)
					return
				}
				slog.Info("scheduled skill completed", "skill", sk.Name)
				if push != nil && text != "" {
					push(text)
				}
			}),
		)
		if err != nil {
			slog.Warn("failed to schedule skill", "skill", sk.Name, "schedule", sk.Schedule, "err", err)
		} else {
			slog.Info("scheduled skill", "skill", sk.Name, "schedule", sk.Schedule)
		}
	}

	return &Scheduler{s: s}, nil
}

// Start begins executing scheduled jobs. Non-blocking.
func (s *Scheduler) Start() {
	s.s.Start()
}

// Stop halts the scheduler and waits for running jobs to finish.
func (s *Scheduler) Stop() error {
	return s.s.Shutdown()
}
