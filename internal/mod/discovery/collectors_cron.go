package discovery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"meshium/internal/mod/transport"
)

// CronCollector collects cron jobs and systemd timers.
type CronCollector struct{}

func (c *CronCollector) Name() string { return "cron" }
func (c *CronCollector) Timeout() time.Duration { return 20 * time.Second }

func (c *CronCollector) Collect(ctx context.Context, exec transport.SSHExecuter) (interface{}, error) {
	result := &cronCollectorResult{}

	if jobs, err := collectCronJobs(ctx, exec); err == nil {
		result.CronJobs = jobs
	} else {
		result.addError("cron_jobs", err)
	}

	if timers, err := collectTimers(ctx, exec); err == nil {
		result.Timers = timers
	} else {
		result.addError("timers", err)
	}

	return result, nil
}

type cronCollectorResult struct {
	CronJobs []CronJobInfo
	Timers   []TimerInfo
	errors   []CollectorError
}

func (r *cronCollectorResult) addError(collector string, err error) {
	if err == nil {
		return
	}
	r.errors = append(r.errors, CollectorError{Collector: collector, Error: err.Error()})
}

func (r *cronCollectorResult) collectorErrors() []CollectorError {
	return append([]CollectorError(nil), r.errors...)
}

func collectCronJobs(ctx context.Context, exec transport.SSHExecuter) ([]CronJobInfo, error) {
	var jobs []CronJobInfo

	passwdOut, err := execText(ctx, exec, `awk -F: '($3 == 0 || $3 >= 1000) {print $1}' /etc/passwd 2>/dev/null`)
	if err == nil {
		for _, user := range strings.Split(passwdOut, "\n") {
			user = strings.TrimSpace(user)
			if user == "" {
				continue
			}
			if out, uerr := execText(ctx, exec, fmt.Sprintf("crontab -l -u %s 2>/dev/null", shellQuote(user))); uerr == nil && out != "" {
				jobs = append(jobs, parseCronLines(out, user, "user")...)
			}
		}
	}

	if out, err := execText(ctx, exec, `cat /etc/crontab /etc/cron.d/* 2>/dev/null`); err == nil && out != "" {
		jobs = append(jobs, parseCronLines(out, "root", "system")...)
	}

	return uniqueCronJobs(jobs), nil
}

func parseCronLines(out, defaultUser, source string) []CronJobInfo {
	var jobs []CronJobInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "PATH=") || strings.HasPrefix(line, "SHELL=") {
			continue
		}
		if strings.HasPrefix(line, "@") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			jobs = append(jobs, CronJobInfo{User: defaultUser, Schedule: parts[0], Command: strings.Join(parts[1:], " "), Source: source})
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		schedule := strings.Join(fields[:5], " ")
		user := defaultUser
		commandStart := 5
		if source != "user" {
			user = fields[5]
			commandStart = 6
		}
		if len(fields) <= commandStart {
			continue
		}
		jobs = append(jobs, CronJobInfo{User: user, Schedule: schedule, Command: strings.Join(fields[commandStart:], " "), Source: source})
	}
	return jobs
}

func uniqueCronJobs(jobs []CronJobInfo) []CronJobInfo {
	seen := make(map[string]struct{}, len(jobs))
	var out []CronJobInfo
	for _, job := range jobs {
		key := job.User + "|" + job.Schedule + "|" + job.Command + "|" + job.Source
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, job)
	}
	return out
}

func collectTimers(ctx context.Context, exec transport.SSHExecuter) ([]TimerInfo, error) {
	out, err := execText(ctx, exec, `systemctl list-timers --all --no-pager --no-legend 2>/dev/null`)
	if err != nil {
		return nil, err
	}
	var timers []TimerInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		timers = append(timers, TimerInfo{
			Name:     fields[4],
			Schedule:  fields[0],
			Interval:  fields[1],
			LastRun:   fields[2],
			NextRun:   fields[0],
			Active:    true,
			Triggers:  fields[5],
		})
	}
	return timers, nil
}

func parseTimerLine(line string) (TimerInfo, bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return TimerInfo{}, false
	}
	_ = strconv.Itoa // keep imports stable if format changes later
	return TimerInfo{}, true
}

func (t TimerInfo) normalize() TimerInfo {
	if t.Schedule == "" {
		t.Schedule = t.NextRun
	}
	return t
}

func sortTimers(timers []TimerInfo) {
	sort.Slice(timers, func(i, j int) bool { return timers[i].Name < timers[j].Name })
}
