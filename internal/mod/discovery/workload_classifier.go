package discovery

import (
	"fmt"
	"strconv"
	"strings"
)

// WorkloadType represents the category of a workload.
type WorkloadType string

const (
	WorkloadStatelessApp  WorkloadType = "stateless_application"
	WorkloadStatefulApp   WorkloadType = "stateful_application"
	WorkloadDatabase      WorkloadType = "database"
	WorkloadCache         WorkloadType = "cache"
	WorkloadQueue         WorkloadType = "queue"
	WorkloadWorker        WorkloadType = "worker"
	WorkloadScheduler     WorkloadType = "scheduler"
	WorkloadReverseProxy  WorkloadType = "reverse_proxy"
	WorkloadMonitoring    WorkloadType = "monitoring"
	WorkloadLogging       WorkloadType = "logging"
	WorkloadStorage       WorkloadType = "storage"
	WorkloadMessaging     WorkloadType = "messaging"
	WorkloadSearch        WorkloadType = "search"
	WorkloadAIService     WorkloadType = "ai_service"
	WorkloadObjectStorage WorkloadType = "object_storage"
	WorkloadUnknown       WorkloadType = "unknown"
)

// WorkloadClassification holds the result of classifying a workload.
type WorkloadClassification struct {
	Name         string       `json:"name"`
	Type         WorkloadType `json:"type"`
	Confidence   float64      `json:"confidence"`
	Reasons      []string     `json:"reasons"`
	Source       string       `json:"source"`
	SourceName   string       `json:"sourceName"`
	Dependencies []string     `json:"dependencies,omitempty"`
	Port         int          `json:"port,omitempty"`
}

// ClassifyWorkloads classifies all workloads in a server snapshot.
func ClassifyWorkloads(snapshot *ServerSnapshot) []WorkloadClassification {
	if snapshot == nil {
		return nil
	}

	results := make([]WorkloadClassification, 0, len(snapshot.Databases)+len(snapshot.Services)+len(snapshot.MessageQueues)+len(snapshot.Monitoring)+len(snapshot.CronJobs)+len(snapshot.ReverseProxies))

	// Classify databases
	for _, db := range snapshot.Databases {
		results = append(results, classifyDatabase(db))
	}

	// Classify Docker containers
	if snapshot.Docker != nil {
		for _, c := range snapshot.Docker.Containers {
			results = append(results, classifyContainer(c, snapshot))
		}
	}

	// Classify systemd services
	for _, svc := range snapshot.Services {
		results = append(results, classifyService(svc, snapshot))
	}

	// Classify Nginx vhosts
	if snapshot.Nginx != nil {
		for _, vh := range snapshot.Nginx.VHosts {
			results = append(results, classifyNginxVHost(vh))
		}
	}

	// Classify message queues
	for _, mq := range snapshot.MessageQueues {
		results = append(results, classifyMessageQueue(mq))
	}

	// Classify monitoring services
	for _, mon := range snapshot.Monitoring {
		results = append(results, classifyMonitoringService(mon))
	}

	// Classify cron jobs
	for _, cron := range snapshot.CronJobs {
		results = append(results, classifyCronJob(cron))
	}

	// Classify reverse proxies
	for _, rp := range snapshot.ReverseProxies {
		results = append(results, classifyReverseProxy(rp))
	}

	return results
}

func classifyWorkloadTypes(snapshot *ServerSnapshot) map[string]WorkloadType {
	results := ClassifyWorkloads(snapshot)
	lookup := make(map[string]WorkloadType, len(results))
	for _, c := range results {
		sourceName := c.SourceName
		if sourceName == "" {
			sourceName = c.Name
		}
		if c.Source == "" || sourceName == "" {
			continue
		}
		lookup[c.Source+":"+sourceName] = c.Type
	}
	return lookup
}

func classifyDatabase(db DatabaseInfo) WorkloadClassification {
	name := db.Type
	if name == "" {
		name = "database"
	}

	normalized := strings.ToLower(strings.TrimSpace(db.Type))
	sourceName := normalized
	if sourceName == "" {
		sourceName = strings.ToLower(strings.TrimSpace(name))
	}

	classification := WorkloadClassification{
		Name:       name,
		Type:       WorkloadUnknown,
		Confidence: 0.15,
		Reasons:    []string{"database type did not match a recognized workload family"},
		Source:     "database",
		SourceName: sourceName,
		Port:       db.Port,
	}

	switch normalized {
	case "mysql", "mariadb", "postgresql", "postgres", "mongodb", "mongo":
		classification.Type = WorkloadDatabase
		classification.Confidence = 0.98
		classification.Reasons = []string{fmt.Sprintf("database type %q is a relational or document database", db.Type)}
	case "redis":
		switch classifyRedisUsage(db.RedisUsage) {
		case WorkloadCache:
			classification.Type = WorkloadCache
			classification.Confidence = 0.97
			classification.Reasons = []string{fmt.Sprintf("redis usage %q indicates a cache", db.RedisUsage)}
		case WorkloadQueue:
			classification.Type = WorkloadQueue
			classification.Confidence = 0.97
			classification.Reasons = []string{fmt.Sprintf("redis usage %q indicates a queue", db.RedisUsage)}
		case WorkloadStatefulApp:
			classification.Type = WorkloadStatefulApp
			classification.Confidence = 0.93
			classification.Reasons = []string{fmt.Sprintf("redis usage %q indicates session/state storage", db.RedisUsage)}
		default:
			classification.Type = WorkloadDatabase
			classification.Confidence = 0.9
			classification.Reasons = []string{"redis without an explicit usage hint is treated as a database"}
		}
	case "elasticsearch", "opensearch":
		classification.Type = WorkloadSearch
		classification.Confidence = 0.97
		classification.Reasons = []string{fmt.Sprintf("database type %q is a search engine", db.Type)}
	case "minio":
		classification.Type = WorkloadObjectStorage
		classification.Confidence = 0.97
		classification.Reasons = []string{"minio provides S3-compatible object storage"}
	}

	return classification
}

func classifyContainer(c ContainerInfo, snapshot *ServerSnapshot) WorkloadClassification {
	displayName := c.Name
	sourceName := c.Name
	if composeService := containerComposeService(c); composeService != "" {
		displayName = composeService
	}

	searchText := strings.ToLower(strings.Join([]string{
		c.Name,
		c.Image,
		c.Status,
		c.State,
		c.Command,
		c.Entrypoint,
		c.ComposeService,
		strings.Join(containerLabelPairs(c.Labels), " "),
		strings.Join(c.Networks, " "),
		strings.Join(c.Volumes, " "),
	}, " "))

	classification := WorkloadClassification{
		Name:         displayName,
		Type:         WorkloadUnknown,
		Confidence:   0.1,
		Reasons:      []string{"no workload-specific patterns matched"},
		Source:       "container",
		SourceName:   sourceName,
		Dependencies: inferContainerDependencies(c),
		Port:         containerPort(c),
	}

	// Reverse proxies.
	if matched, reason := classifyProxyFamily(searchText); matched {
		classification.Type = WorkloadReverseProxy
		classification.Confidence = 0.98
		classification.Reasons = []string{reason}
		return classification
	}

	// Databases and storage systems.
	if matched, reason := classifyDatabaseFamily(searchText); matched {
		classification.Type = WorkloadDatabase
		classification.Confidence = 0.98
		classification.Reasons = []string{reason}
		return classification
	}
	if matched, reason := classifyRedisContainer(searchText, c); matched {
		classification.Type = reason.workloadType
		classification.Confidence = reason.confidence
		classification.Reasons = []string{reason.reason}
		return classification
	}
	if matched, reason := classifySearchFamily(searchText); matched {
		classification.Type = reason.workloadType
		classification.Confidence = reason.confidence
		classification.Reasons = []string{reason.reason}
		return classification
	}
	if matched, reason := classifyObjectStorageFamily(searchText); matched {
		classification.Type = WorkloadObjectStorage
		classification.Confidence = 0.97
		classification.Reasons = []string{reason}
		return classification
	}

	// Messaging and workers.
	if matched, reason := classifyQueueFamily(searchText); matched {
		classification.Type = WorkloadQueue
		classification.Confidence = 0.97
		classification.Reasons = []string{reason}
		return classification
	}
	if matched, reason := classifyWorkerFamily(searchText); matched {
		classification.Type = WorkloadWorker
		classification.Confidence = 0.95
		classification.Reasons = []string{reason}
		return classification
	}

	// Monitoring and logging.
	if matched, reason, typ := classifyObservabilityFamily(searchText); matched {
		classification.Type = typ
		classification.Confidence = 0.96
		classification.Reasons = []string{reason}
		return classification
	}

	// PM2 can represent either a worker or an app.
	if strings.Contains(searchText, "pm2") {
		if looksLikeWorker(searchText) {
			classification.Type = WorkloadWorker
			classification.Confidence = 0.87
			classification.Reasons = []string{"pm2 is running a worker-oriented container"}
			return classification
		}
		if looksLikeApplicationContainer(c, searchText) {
			return classifyGenericApplicationContainer(c, displayName, sourceName)
		}
	}

	// Generic application containers.
	if looksLikeApplicationContainer(c, searchText) {
		return classifyGenericApplicationContainer(c, displayName, sourceName)
	}

	return classification
}

func classifyService(svc SystemService, snapshot *ServerSnapshot) WorkloadClassification {
	name := strings.TrimSpace(svc.Name)
	normalized := strings.ToLower(trimServiceSuffix(name))
	classification := WorkloadClassification{
		Name:       name,
		Type:       WorkloadUnknown,
		Confidence: 0.1,
		Reasons:    []string{"service name did not match a recognized workload family"},
		Source:     "service",
		SourceName: name,
	}

	switch {
	case containsAny(normalized, "nginx", "caddy", "traefik", "haproxy", "apache", "httpd", "envoy"):
		classification.Type = WorkloadReverseProxy
		classification.Confidence = 0.97
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a reverse proxy", svc.Name)}
	case containsAny(normalized, "mysql", "mariadb", "postgres", "postgresql", "mongodb", "mongo"):
		classification.Type = WorkloadDatabase
		classification.Confidence = 0.97
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a database", svc.Name)}
	case strings.Contains(normalized, "redis"):
		classification.Type = WorkloadCache
		classification.Confidence = 0.92
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a Redis cache", svc.Name)}
	case containsAny(normalized, "rabbitmq", "kafka", "nats", "activemq", "beanstalkd", "nsq", "redpanda"):
		classification.Type = WorkloadQueue
		classification.Confidence = 0.95
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a message queue", svc.Name)}
	case containsAny(normalized, "prometheus", "grafana", "loki", "jaeger", "alertmanager", "telegraf", "cadvisor", "node_exporter", "tempo"):
		classification.Type = WorkloadMonitoring
		classification.Confidence = 0.95
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a monitoring component", svc.Name)}
	case containsAny(normalized, "fluentd", "fluent-bit", "logstash", "graylog", "vector", "syslog"):
		classification.Type = WorkloadLogging
		classification.Confidence = 0.95
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a logging component", svc.Name)}
	case containsAny(normalized, "elasticsearch", "opensearch", "solr", "meilisearch"):
		classification.Type = WorkloadSearch
		classification.Confidence = 0.95
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a search service", svc.Name)}
	case containsAny(normalized, "minio", "seaweedfs"):
		classification.Type = WorkloadObjectStorage
		classification.Confidence = 0.95
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates object storage", svc.Name)}
	case containsAny(normalized, "cron", "timer", "scheduler", "backup", "job"):
		classification.Type = WorkloadScheduler
		classification.Confidence = 0.9
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates scheduled work", svc.Name)}
	case containsAny(normalized, "worker", "celery", "sidekiq", "resque", "bullmq", "rq", "dramatiq"):
		classification.Type = WorkloadWorker
		classification.Confidence = 0.95
		classification.Reasons = []string{fmt.Sprintf("service name %q indicates a worker process", svc.Name)}
	}

	return classification
}

func classifyNginxVHost(vh NginxVHost) WorkloadClassification {
	name := vh.ServerName
	if name == "" {
		name = vh.Listen
	}
	if name == "" {
		name = vh.ConfigFile
	}

	classification := WorkloadClassification{
		Name:       name,
		Type:       WorkloadReverseProxy,
		Confidence: 0.98,
		Reasons:    []string{"Nginx virtual hosts are reverse proxy endpoints"},
		Source:     "nginx",
		SourceName: vh.ServerName,
		Port:       parseListenPort(vh.Listen),
	}
	if classification.SourceName == "" {
		classification.SourceName = vh.Listen
	}
	if classification.SourceName == "" {
		classification.SourceName = vh.ConfigFile
	}
	return classification
}

func classifyMessageQueue(mq MessageQueueInfo) WorkloadClassification {
	name := strings.TrimSpace(mq.Type)
	if name == "" {
		name = strings.TrimSpace(mq.Version)
	}
	if name == "" {
		name = "message queue"
	}
	text := strings.ToLower(strings.Join([]string{name, mq.Version}, " "))

	classification := WorkloadClassification{
		Name:       name,
		Type:       WorkloadUnknown,
		Confidence: 0.15,
		Reasons:    []string{"message queue type did not match a recognized broker"},
		Source:     "message_queue",
		SourceName: strings.ToLower(strings.TrimSpace(mq.Type)),
		Port:       mq.Port,
	}
	if classification.SourceName == "" {
		classification.SourceName = strings.ToLower(strings.TrimSpace(name))
	}

	switch {
	case containsAny(text, "rabbitmq", "kafka", "nats", "activemq", "beanstalkd", "nsq", "redpanda"):
		classification.Type = WorkloadQueue
		classification.Confidence = 0.97
		classification.Reasons = []string{fmt.Sprintf("message queue type %q maps to a queue broker", mq.Type)}
	case containsAny(text, "mqtt", "pubsub", "servicebus", "sns", "sqs", "broker"):
		classification.Type = WorkloadMessaging
		classification.Confidence = 0.93
		classification.Reasons = []string{fmt.Sprintf("message queue type %q maps to a messaging service", mq.Type)}
	}

	return classification
}

func classifyMonitoringService(mon MonitoringServiceInfo) WorkloadClassification {
	name := strings.TrimSpace(mon.Type)
	if name == "" {
		name = strings.TrimSpace(mon.Version)
	}
	if name == "" {
		name = "monitoring service"
	}
	text := strings.ToLower(strings.Join([]string{name, mon.Version}, " "))

	classification := WorkloadClassification{
		Name:       name,
		Type:       WorkloadUnknown,
		Confidence: 0.15,
		Reasons:    []string{"monitoring service type did not match a recognized observability component"},
		Source:     "monitoring",
		SourceName: strings.ToLower(strings.TrimSpace(mon.Type)),
		Port:       mon.Port,
	}
	if classification.SourceName == "" {
		classification.SourceName = strings.ToLower(strings.TrimSpace(name))
	}

	switch {
	case containsAny(text, "prometheus", "grafana", "loki", "jaeger", "alertmanager", "telegraf", "cadvisor", "node_exporter", "tempo", "opentelemetry", "otel"):
		classification.Type = WorkloadMonitoring
		classification.Confidence = 0.97
		classification.Reasons = []string{fmt.Sprintf("monitoring type %q is an observability component", mon.Type)}
	case containsAny(text, "fluentd", "fluent-bit", "logstash", "graylog", "vector", "syslog"):
		classification.Type = WorkloadLogging
		classification.Confidence = 0.96
		classification.Reasons = []string{fmt.Sprintf("monitoring type %q is a logging component", mon.Type)}
	}

	return classification
}

func classifyCronJob(cron CronJobInfo) WorkloadClassification {
	name := strings.TrimSpace(cron.Source)
	if name == "" {
		name = strings.TrimSpace(cron.Command)
	}
	if name == "" {
		name = "cron job"
	}

	return WorkloadClassification{
		Name:       name,
		Type:       WorkloadScheduler,
		Confidence: 0.96,
		Reasons:    []string{"cron entries represent scheduled workload execution"},
		Source:     "cron",
		SourceName: strings.TrimSpace(cron.Source),
	}
}

func classifyReverseProxy(rp ReverseProxyInfo) WorkloadClassification {
	name := strings.TrimSpace(rp.Type)
	if name == "" {
		name = strings.TrimSpace(rp.ConfigPath)
	}
	if name == "" {
		name = "reverse proxy"
	}

	return WorkloadClassification{
		Name:       name,
		Type:       WorkloadReverseProxy,
		Confidence: confidenceIf(rp.Active, 0.97, 0.85),
		Reasons:    []string{fmt.Sprintf("%s is a reverse proxy component", name)},
		Source:     "reverse_proxy",
		SourceName: strings.TrimSpace(rp.Type),
	}
}

func classifyGenericApplicationContainer(c ContainerInfo, displayName, sourceName string) WorkloadClassification {
	if hasPersistentVolumes(c) {
		return WorkloadClassification{
			Name:         displayName,
			Type:         WorkloadStatefulApp,
			Confidence:   0.78,
			Reasons:      []string{"container has persistent volume mounts, so it is likely stateful"},
			Source:       "container",
			SourceName:   sourceName,
			Dependencies: inferContainerDependencies(c),
			Port:         containerPort(c),
		}
	}

	return WorkloadClassification{
		Name:         displayName,
		Type:         WorkloadStatelessApp,
		Confidence:   0.74,
		Reasons:      []string{"container looks application-like and has no persistent volume mounts"},
		Source:       "container",
		SourceName:   sourceName,
		Dependencies: inferContainerDependencies(c),
		Port:         containerPort(c),
	}
}

type redisContainerClassification struct {
	workloadType WorkloadType
	confidence   float64
	reason       string
}

func classifyRedisContainer(searchText string, c ContainerInfo) (bool, redisContainerClassification) {
	if !strings.Contains(searchText, "redis") {
		return false, redisContainerClassification{}
	}

	switch classifyRedisUsage(containerRedisUsage(c)) {
	case WorkloadCache:
		return true, redisContainerClassification{
			workloadType: WorkloadCache,
			confidence:   0.97,
			reason:       "redis container appears to be used as a cache",
		}
	case WorkloadQueue:
		return true, redisContainerClassification{
			workloadType: WorkloadQueue,
			confidence:   0.97,
			reason:       "redis container appears to be used as a queue",
		}
	case WorkloadStatefulApp:
		return true, redisContainerClassification{
			workloadType: WorkloadStatefulApp,
			confidence:   0.94,
			reason:       "redis container appears to be session/state storage",
		}
	default:
		return true, redisContainerClassification{
			workloadType: WorkloadDatabase,
			confidence:   0.9,
			reason:       "redis container has no usage hint, so it is treated as a database",
		}
	}
}

type searchClassification struct {
	workloadType WorkloadType
	confidence   float64
	reason       string
}

func classifySearchFamily(searchText string) (bool, searchClassification) {
	if !containsAny(searchText, "elasticsearch", "opensearch", "solr", "meilisearch") {
		return false, searchClassification{}
	}
	return true, searchClassification{
		workloadType: WorkloadSearch,
		confidence:   0.97,
		reason:       "container name or image indicates a search engine",
	}
}

func classifyObjectStorageFamily(searchText string) (bool, string) {
	if containsAny(searchText, "minio", "seaweedfs", "s3") {
		return true, "container name or image indicates object storage"
	}
	return false, ""
}

func classifyDatabaseFamily(searchText string) (bool, string) {
	if containsAny(searchText, "mysql", "mariadb", "postgres", "postgresql", "mongodb", "mongo") {
		return true, "container name or image indicates a database server"
	}
	return false, ""
}

func classifyProxyFamily(searchText string) (bool, string) {
	if containsAny(searchText, "nginx", "caddy", "traefik", "haproxy", "apache", "httpd", "envoy") {
		return true, "container name or image indicates a reverse proxy"
	}
	return false, ""
}

func classifyQueueFamily(searchText string) (bool, string) {
	if containsAny(searchText, "rabbitmq", "kafka", "nats", "activemq", "beanstalkd", "nsq", "redpanda") {
		return true, "container name or image indicates a queue broker"
	}
	return false, ""
}

func classifyWorkerFamily(searchText string) (bool, string) {
	if looksLikeWorker(searchText) {
		return true, "container name or image indicates a background worker"
	}
	return false, ""
}

func classifyObservabilityFamily(searchText string) (bool, string, WorkloadType) {
	switch {
	case containsAny(searchText, "prometheus", "grafana", "loki", "jaeger", "alertmanager", "telegraf", "cadvisor", "node_exporter", "tempo", "opentelemetry", "otel"):
		return true, "container name or image indicates observability tooling", WorkloadMonitoring
	case containsAny(searchText, "fluentd", "fluent-bit", "logstash", "graylog", "vector", "syslog"):
		return true, "container name or image indicates logging tooling", WorkloadLogging
	}
	return false, "", WorkloadUnknown
}

func inferContainerDependencies(c ContainerInfo) []string {
	var deps []string
	for _, env := range c.EnvVarNames {
		u := strings.ToUpper(strings.TrimSpace(env))
		switch {
		case strings.Contains(u, "DATABASE_URL"), strings.Contains(u, "DB_HOST"), strings.Contains(u, "POSTGRES"), strings.Contains(u, "MYSQL"):
			deps = appendUniqueString(deps, "database")
		case strings.Contains(u, "REDIS_URL"), strings.Contains(u, "CACHE_URL"), strings.Contains(u, "SESSION"):
			deps = appendUniqueString(deps, "cache")
		case strings.Contains(u, "RABBITMQ"), strings.Contains(u, "KAFKA"), strings.Contains(u, "NATS"), strings.Contains(u, "QUEUE"):
			deps = appendUniqueString(deps, "queue")
		case strings.Contains(u, "ELASTIC"), strings.Contains(u, "OPENSEARCH"), strings.Contains(u, "SEARCH"):
			deps = appendUniqueString(deps, "search")
		case strings.Contains(u, "S3"), strings.Contains(u, "MINIO"), strings.Contains(u, "OBJECT_STORAGE"):
			deps = appendUniqueString(deps, "object_storage")
		}
	}
	return deps
}

func classifyRedisUsage(usage string) WorkloadType {
	switch strings.ToLower(strings.TrimSpace(usage)) {
	case "cache":
		return WorkloadCache
	case "bullmq", "queue", "queues", "job", "jobs", "worker", "workers":
		return WorkloadQueue
	case "session", "sessions", "store", "state", "stateful":
		return WorkloadStatefulApp
	default:
		return WorkloadDatabase
	}
}

func containerRedisUsage(c ContainerInfo) string {
	parts := []string{c.Name, c.Image, c.Status, c.State, c.Command, c.Entrypoint, c.ComposeService}
	parts = append(parts, containerLabelPairs(c.Labels)...)
	parts = append(parts, c.EnvVarNames...)
	parts = append(parts, c.Volumes...)
	text := strings.ToLower(strings.Join(parts, " "))
	switch {
	case containsAny(text, "bullmq", "queue", "queues", "job", "jobs", "worker", "workers"):
		return "bullmq"
	case containsAny(text, "cache", "caching"):
		return "cache"
	case containsAny(text, "session", "sessions"):
		return "session"
	default:
		return ""
	}
}

func looksLikeApplicationContainer(c ContainerInfo, searchText string) bool {
	if c.ComposeService != "" || containerComposeService(c) != "" {
		return true
	}
	if len(c.Ports) > 0 || len(c.Volumes) > 0 || len(c.EnvVarNames) > 0 {
		return true
	}
	return containsAny(searchText,
		"app",
		"web",
		"api",
		"frontend",
		"backend",
		"portal",
		"site",
		"service",
		"gateway",
		"ui",
		"client",
		"pm2",
		"node",
		"python",
		"php",
		"ruby",
		"go",
		"java",
		"spring",
		"django",
		"rails",
		"laravel",
	)
}

func looksLikeWorker(searchText string) bool {
	return containsAny(searchText, "worker", "celery", "sidekiq", "resque", "bullmq", "rq", "dramatiq", "job-runner", "task-runner")
}

func hasPersistentVolumes(c ContainerInfo) bool {
	return len(c.Volumes) > 0
}

func containerPort(c ContainerInfo) int {
	if len(c.Ports) == 0 {
		return 0
	}
	if c.Ports[0].ContainerPort > 0 {
		return c.Ports[0].ContainerPort
	}
	return c.Ports[0].HostPort
}

func containerComposeService(c ContainerInfo) string {
	if c.ComposeService != "" {
		return strings.TrimSpace(c.ComposeService)
	}
	if c.Labels == nil {
		return ""
	}
	return strings.TrimSpace(c.Labels["com.docker.compose.service"])
}

func containerLabelPairs(labels map[string]string) []string {
	if len(labels) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(labels)*2)
	for k, v := range labels {
		pairs = append(pairs, k, v)
	}
	return pairs
}

func parseListenPort(listen string) int {
	listen = strings.TrimSpace(listen)
	if listen == "" {
		return 0
	}

	fields := strings.Fields(listen)
	for i := len(fields) - 1; i >= 0; i-- {
		token := strings.Trim(fields[i], ";[]()")
		if idx := strings.LastIndex(token, ":"); idx >= 0 {
			token = token[idx+1:]
		}
		if port, err := strconv.Atoi(token); err == nil {
			return port
		}
	}
	return 0
}

func trimServiceSuffix(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	for _, suffix := range []string{".service", ".socket", ".timer", ".target", ".path"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, candidate string) []string {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func confidenceIf(ok bool, yes, no float64) float64 {
	if ok {
		return yes
	}
	return no
}
