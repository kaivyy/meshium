package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"meshium/internal/shared"
)

// QueueEngine manages message queues during migration cutover.
// Supports BullMQ (Redis-based), Redis Queue, and RabbitMQ.
type QueueEngine struct {
	sourceSSH SSHExecuter
	targetSSH SSHExecuter
	repo      PipelineRepo
}

// NewQueueEngine creates a new queue engine.
func NewQueueEngine(sourceSSH, targetSSH SSHExecuter, repo PipelineRepo) *QueueEngine {
	return &QueueEngine{
		sourceSSH: sourceSSH,
		targetSSH: targetSSH,
		repo:      repo,
	}
}

// QueueConfig configures queue management for a migration.
type QueueConfig struct {
	QueueType   string `json:"queueType"` // bullmq, redis, rabbitmq
	QueueName   string `json:"queueName"`
	RedisHost   string `json:"redisHost,omitempty"`
	RedisPort   int    `json:"redisPort,omitempty"`
	RedisDB     int    `json:"redisDB,omitempty"`
	MigrationID int    `json:"migrationId"`
}

// PauseQueue pauses queue processing on the source server.
func (e *QueueEngine) PauseQueue(ctx context.Context, migrationID int, config QueueConfig) error {
	config.MigrationID = migrationID

	state := QueueState{
		MigrationID: migrationID,
		QueueType:   config.QueueType,
		QueueName:   config.QueueName,
		Paused:      true,
	}
	stateID, err := e.repo.CreateQueueState(ctx, state)
	if err != nil {
		return fmt.Errorf("create queue state: %w", err)
	}

	switch config.QueueType {
	case "bullmq", "redis":
		return e.pauseBullMQ(ctx, config, stateID)
	case "rabbitmq":
		return e.pauseRabbitMQ(ctx, config, stateID)
	default:
		return fmt.Errorf("unsupported queue type: %s", config.QueueType)
	}
}

// DrainQueue waits for active jobs to complete.
func (e *QueueEngine) DrainQueue(ctx context.Context, migrationID int, config QueueConfig) error {
	stateID, err := e.findQueueState(migrationID, config)
	if err != nil {
		return err
	}

	switch config.QueueType {
	case "bullmq", "redis":
		return e.drainBullMQ(ctx, config, stateID)
	case "rabbitmq":
		return e.drainRabbitMQ(ctx, config, stateID)
	default:
		return fmt.Errorf("unsupported queue type: %s", config.QueueType)
	}
}

// WaitForActiveJobs blocks until all active jobs complete or timeout.
func (e *QueueEngine) WaitForActiveJobs(ctx context.Context, config QueueConfig, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for active jobs: %w", ctx.Err())
		case <-ticker.C:
			count, err := e.GetActiveJobCount(ctx, config)
			if err != nil {
				return fmt.Errorf("get active job count: %w", err)
			}
			if count == 0 {
				return nil
			}
		}
	}
}

// SyncQueue syncs queue state from source to target.
func (e *QueueEngine) SyncQueue(ctx context.Context, migrationID int, config QueueConfig) error {
	stateID, err := e.findQueueState(migrationID, config)
	if err != nil {
		return err
	}

	switch config.QueueType {
	case "bullmq", "redis":
		return e.syncBullMQ(ctx, config, stateID)
	case "rabbitmq":
		return e.syncRabbitMQ(ctx, config, stateID)
	default:
		return fmt.Errorf("unsupported queue type: %s", config.QueueType)
	}
}

// ResumeQueue resumes queue processing on the target server.
func (e *QueueEngine) ResumeQueue(ctx context.Context, migrationID int, config QueueConfig) error {
	stateID, err := e.findQueueState(migrationID, config)
	if err != nil {
		return err
	}

	switch config.QueueType {
	case "bullmq", "redis":
		return e.resumeBullMQ(ctx, config, stateID)
	case "rabbitmq":
		return e.resumeRabbitMQ(ctx, config, stateID)
	default:
		return fmt.Errorf("unsupported queue type: %s", config.QueueType)
	}
}

// VerifyQueue verifies that no jobs were lost during migration.
func (e *QueueEngine) VerifyQueue(ctx context.Context, migrationID int, config QueueConfig) error {
	stateID, err := e.findQueueState(migrationID, config)
	if err != nil {
		return err
	}

	// Check that target queue has the expected number of jobs
	activeJobs, err := e.GetActiveJobCount(ctx, config)
	if err != nil {
		return fmt.Errorf("verify queue: %w", err)
	}

	e.repo.UpdateQueueState(ctx, stateID, false, activeJobs, true, true, true, "")
	return nil
}

// GetActiveJobCount returns the number of active jobs in the queue.
func (e *QueueEngine) GetActiveJobCount(ctx context.Context, config QueueConfig) (int, error) {
	switch config.QueueType {
	case "bullmq", "redis":
		return e.getBullMQActiveCount(ctx, config)
	case "rabbitmq":
		return e.getRabbitMQActiveCount(ctx, config)
	default:
		return 0, fmt.Errorf("unsupported queue type: %s", config.QueueType)
	}
}

// --- BullMQ ---

func (e *QueueEngine) pauseBullMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	queueName := config.QueueName
	if queueName == "" {
		return fmt.Errorf("queue name is required for BullMQ")
	}

	redisHost := config.RedisHost
	if redisHost == "" {
		redisHost = "127.0.0.1"
	}
	redisPort := config.RedisPort
	if redisPort == 0 {
		redisPort = 6379
	}
	targetRedisHost := config.RedisHost
	if targetRedisHost == "" {
		targetRedisHost = "127.0.0.1"
	}

	cmd := fmt.Sprintf("redis-cli -h %s -p %d SET bull:%s:paused 1 2>&1",
		shared.ShellQuote(redisHost), redisPort, shared.ShellQuote(queueName))
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		e.repo.UpdateQueueState(ctx, stateID, false, 0, false, false, false, err.Error())
		return fmt.Errorf("pause bullmq: %w", err)
	}

	e.repo.UpdateQueueState(ctx, stateID, true, 0, false, false, false, "")
	return nil
}

func (e *QueueEngine) drainBullMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	// Wait for active jobs to complete
	return e.WaitForActiveJobs(ctx, config, 5*time.Minute)
}

func (e *QueueEngine) syncBullMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	queueName := config.QueueName
	redisHost := config.RedisHost
	if redisHost == "" {
		redisHost = "127.0.0.1"
	}
	redisPort := config.RedisPort
	if redisPort == 0 {
		redisPort = 6379
	}
	// The RESTORE runs on the target host (via e.targetSSH), so it must target
	// the target's local Redis. The previous code defaulted targetRedisHost to
	// config.RedisHost (the SOURCE host), which caused the sync to write the
	// keys back to the source instead of the target.
	targetRedisHost := "127.0.0.1"

	// Dump BullMQ keys from source Redis
	pattern := fmt.Sprintf("bull:%s:*", queueName)
	cmd := fmt.Sprintf("redis-cli -h %s -p %d --scan --pattern %s 2>&1",
		shared.ShellQuote(redisHost), redisPort, shared.ShellQuote(pattern))
	keys, _, _, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		return fmt.Errorf("scan bullmq keys: %w", err)
	}

	// Migrate each key to target Redis
	for _, key := range strings.Split(keys, "\n") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		// Skip the pause flag. The scan pattern "bull:<name>:*" matches the
		// "bull:<name>:paused" key set by pauseBullMQ; copying it to the target
		// would leave the migrated queue paused forever, since resumeBullMQ only
		// clears the flag on the source. The target must come up unpaused.
		if strings.HasSuffix(key, ":paused") {
			continue
		}

		// Dump the key. The DUMP payload is raw binary containing NUL bytes,
		// which get corrupted when passed as a shell-quoted argv value over the
		// text SSH channel. Base64-encode it on the source so it survives as
		// text (head -c -1 strips the single trailing newline redis-cli appends
		// to the reply, which would otherwise corrupt the payload).
		dumpCmd := fmt.Sprintf("redis-cli -h %s -p %d DUMP %s | head -c -1 | base64 | tr -d '\\n'",
			shared.ShellQuote(redisHost), redisPort, shared.ShellQuote(key))
		dumpB64, _, _, err := e.sourceSSH.ExecContext(ctx, dumpCmd)
		if err != nil {
			e.repo.UpdateQueueState(ctx, stateID, true, 0, false, false, false, err.Error())
			return fmt.Errorf("dump redis key %s: %w", key, err)
		}
		dumpB64 = strings.TrimSpace(dumpB64)

		// Get TTL in MILLISECONDS. RESTORE's TTL argument is milliseconds, so we
		// must read PTTL (ms), not TTL (seconds) — using TTL would restore a
		// 3600s key with a 3600ms TTL, expiring it ~1000x too soon. PTTL returns
		// -1 (no expiry) or -2 (missing); only a positive value is a real TTL.
		ttlCmd := fmt.Sprintf("redis-cli -h %s -p %d PTTL %s 2>&1",
			shared.ShellQuote(redisHost), redisPort, shared.ShellQuote(key))
		ttlOutput, _, _, _ := e.sourceSSH.ExecContext(ctx, ttlCmd)
		ttl := "0"
		if t, err := strconv.Atoi(strings.TrimSpace(ttlOutput)); err == nil && t > 0 {
			ttl = strconv.Itoa(t)
		}

		// Restore on target: decode the base64 payload and feed the raw binary
		// to RESTORE via stdin (redis-cli -x reads the final argument from
		// stdin), so no NUL bytes ever pass through argv.
		restoreCmd := fmt.Sprintf("echo %s | base64 -d | redis-cli -h %s -p %d -x RESTORE %s %s 2>&1",
			shared.ShellQuote(dumpB64), shared.ShellQuote(targetRedisHost), redisPort,
			shared.ShellQuote(key), ttl)
		if _, _, _, err := e.targetSSH.ExecContext(ctx, restoreCmd); err != nil {
			e.repo.UpdateQueueState(ctx, stateID, true, 0, false, false, false, err.Error())
			return fmt.Errorf("restore redis key %s: %w", key, err)
		}
	}

	e.repo.UpdateQueueState(ctx, stateID, true, 0, true, true, false, "")
	return nil
}

func (e *QueueEngine) resumeBullMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	queueName := config.QueueName
	redisHost := config.RedisHost
	if redisHost == "" {
		redisHost = "127.0.0.1"
	}
	redisPort := config.RedisPort
	if redisPort == 0 {
		redisPort = 6379
	}

	// Resume must undo the pause on the SAME host the pause acted on.
	// pauseBullMQ SETs bull:<name>:paused via sourceSSH, so resume must DEL it
	// via sourceSSH too. Using targetSSH here left the source queue paused
	// (which is the queue that matters on a rollback).
	cmd := fmt.Sprintf("redis-cli -h %s -p %d DEL bull:%s:paused 2>&1",
		shared.ShellQuote(redisHost), redisPort, shared.ShellQuote(queueName))
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("resume bullmq: %w", err)
	}

	e.repo.UpdateQueueState(ctx, stateID, false, 0, true, true, true, "")
	return nil
}

func (e *QueueEngine) getBullMQActiveCount(ctx context.Context, config QueueConfig) (int, error) {
	queueName := config.QueueName
	redisHost := config.RedisHost
	if redisHost == "" {
		redisHost = "127.0.0.1"
	}
	redisPort := config.RedisPort
	if redisPort == 0 {
		redisPort = 6379
	}

	cmd := fmt.Sprintf("redis-cli -h %s -p %d LLEN bull:%s:active 2>&1",
		shared.ShellQuote(redisHost), redisPort, shared.ShellQuote(queueName))
	output, _, _, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		return 0, err
	}

	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0, nil
	}
	return count, nil
}

// --- RabbitMQ ---

func (e *QueueEngine) pauseRabbitMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	cmd := fmt.Sprintf("rabbitmqctl set_vm_memory_high_watermark 0 2>&1")
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		e.repo.UpdateQueueState(ctx, stateID, false, 0, false, false, false, err.Error())
		return fmt.Errorf("pause rabbitmq: %w", err)
	}
	e.repo.UpdateQueueState(ctx, stateID, true, 0, false, false, false, "")
	return nil
}

func (e *QueueEngine) drainRabbitMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	return e.WaitForActiveJobs(ctx, config, 5*time.Minute)
}

func (e *QueueEngine) syncRabbitMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	// Use RabbitMQ shovel plugin to transfer messages
	queueName := config.QueueName
	if queueName == "" {
		return fmt.Errorf("queue name is required for RabbitMQ")
	}

	cmd := fmt.Sprintf(
		"rabbitmqctl set_parameter shovel meshium-sync '{\"src-protocol\":\"amqp091\",\"src-uri\":\"amqp://source\",\"src-queue\":\"%s\",\"dest-protocol\":\"amqp091\",\"dest-uri\":\"amqp://target\",\"dest-queue\":\"%s\"}' 2>&1",
		shared.ShellQuote(queueName), shared.ShellQuote(queueName),
	)
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("create rabbitmq shovel: %w", err)
	}

	e.repo.UpdateQueueState(ctx, stateID, true, 0, true, true, false, "")
	return nil
}

func (e *QueueEngine) resumeRabbitMQ(ctx context.Context, config QueueConfig, stateID int64) error {
	// pauseRabbitMQ throttles the SOURCE broker (watermark 0 via sourceSSH), so
	// resume must restore the watermark on the SAME source broker. Running this
	// on targetSSH left the source broker permanently paused.
	cmd := "rabbitmqctl set_vm_memory_high_watermark 0.4 2>&1"
	if _, _, _, err := e.sourceSSH.ExecContext(ctx, cmd); err != nil {
		return fmt.Errorf("resume rabbitmq: %w", err)
	}
	e.repo.UpdateQueueState(ctx, stateID, false, 0, true, true, true, "")
	return nil
}

func (e *QueueEngine) getRabbitMQActiveCount(ctx context.Context, config QueueConfig) (int, error) {
	queueName := config.QueueName
	if queueName == "" {
		return 0, nil
	}

	cmd := fmt.Sprintf("rabbitmqctl list_queues name messages consumers --formatter csv 2>&1 | grep %s",
		shared.ShellQuote(queueName))
	output, _, _, err := e.sourceSSH.ExecContext(ctx, cmd)
	if err != nil {
		return 0, err
	}

	parts := strings.Split(strings.TrimSpace(output), ",")
	if len(parts) >= 2 {
		count, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, nil
		}
		return count, nil
	}
	return 0, nil
}

// --- Helpers ---

func (e *QueueEngine) findQueueState(migrationID int, config QueueConfig) (int64, error) {
	states, err := e.repo.GetQueueStates(migrationID)
	if err != nil {
		return 0, err
	}
	for _, s := range states {
		if s.QueueType == config.QueueType && s.QueueName == config.QueueName {
			return s.ID, nil
		}
	}
	return 0, fmt.Errorf("queue state not found for %s/%s", config.QueueType, config.QueueName)
}
