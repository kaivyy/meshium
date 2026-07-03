package discovery

import (
	"fmt"
	"strconv"
	"strings"
)

// --- Dependency Graph Types ---

// DependencyNode represents a single node in the dependency graph.
// A node can be a Docker container, a systemd service, a database,
// an Nginx virtual host, or other discovered workload.
type DependencyNode struct {
	// ID is a unique identifier for the node.
	ID string `json:"id"`
	// Name is the human-readable name.
	Name string `json:"name"`
	// Type is the node type: "container", "service", "database", "nginx",
	// "queue", "cache", "monitoring", "cron", "reverse_proxy", etc.
	Type string `json:"type"`
	// WorkloadType classifies the workload represented by this node.
	WorkloadType WorkloadType `json:"workloadType,omitempty"`
	// Port is the port the node listens on (if applicable).
	Port int `json:"port,omitempty"`
	// Metadata holds additional key-value data about the node.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// DependencyEdge represents a directional dependency between two nodes.
// An edge from A → B means "A depends on B".
type DependencyEdge struct {
	// From is the source node ID (the dependent).
	From string `json:"from"`
	// To is the destination node ID (the dependency).
	To string `json:"to"`
	// Reason explains why the dependency exists.
	Reason string `json:"reason"`
}

// DependencyGraph is a directed graph of service dependencies.
// It is used by the migration planner (Phase 5) to determine
// the order in which services should be migrated.
type DependencyGraph struct {
	Nodes []DependencyNode `json:"nodes"`
	Edges []DependencyEdge `json:"edges"`
}

// --- Dependency Graph Builder ---

// BuildDependencyGraph constructs a dependency graph from a ServerSnapshot.
//
// The graph captures the following relationships:
//   - Docker container → Database: if a container exposes a port that
//     matches a detected database's port.
//   - Nginx vhost → Docker container: if an Nginx proxy_pass or upstream
//     points to a port that a container exposes.
//   - Systemd service → Systemd service: from After/Requires directives.
//   - Systemd service → Database: if a service depends on a database
//     by name convention (e.g., "mysql.service" → MySQL database).
func BuildDependencyGraph(snapshot *ServerSnapshot) *DependencyGraph {
	if snapshot == nil {
		return &DependencyGraph{}
	}

	g := &DependencyGraph{}
	workloadTypes := classifyWorkloadTypes(snapshot)

	// Create nodes for Docker containers
	if snapshot.Docker != nil {
		for _, c := range snapshot.Docker.Containers {
			node := DependencyNode{
				ID:           "container:" + c.Name,
				Name:         c.Name,
				Type:         "container",
				WorkloadType: workloadTypes["container:"+c.Name],
			}
			// Use the first exposed container port
			if len(c.Ports) > 0 {
				node.Port = c.Ports[0].ContainerPort
			}
			g.Nodes = append(g.Nodes, node)
		}
	}

	// Create nodes for databases
	for _, db := range snapshot.Databases {
		g.Nodes = append(g.Nodes, DependencyNode{
			ID:           "database:" + db.Type,
			Name:         db.Type,
			Type:         "database",
			WorkloadType: workloadTypes["database:"+db.Type],
			Port:         db.Port,
		})
	}

	// Create nodes for systemd services
	for _, svc := range snapshot.Services {
		g.Nodes = append(g.Nodes, DependencyNode{
			ID:           "service:" + svc.Name,
			Name:         svc.Name,
			Type:         "service",
			WorkloadType: workloadTypes["service:"+svc.Name],
		})
	}

	// Create nodes for Nginx vhosts
	if snapshot.Nginx != nil {
		for _, vh := range snapshot.Nginx.VHosts {
			g.Nodes = append(g.Nodes, DependencyNode{
				ID:           "nginx:" + vh.ServerName,
				Name:         vh.ServerName,
				Type:         "nginx",
				WorkloadType: workloadTypes["nginx:"+vh.ServerName],
			})
		}
	}

	// Build edges
	g.buildContainerDatabaseEdges(snapshot)
	g.buildNginxContainerEdges(snapshot)
	g.buildServiceDependencies(snapshot)
	g.buildServiceDatabaseEdges(snapshot)
	g.buildEnvVarEdges(snapshot)
	g.buildVolumeSharingEdges(snapshot)

	return g
}

// buildContainerDatabaseEdges links containers to databases by port.
func (g *DependencyGraph) buildContainerDatabaseEdges(snapshot *ServerSnapshot) {
	if snapshot.Docker == nil || len(snapshot.Databases) == 0 {
		return
	}

	for _, c := range snapshot.Docker.Containers {
		for _, pm := range c.Ports {
			for _, db := range snapshot.Databases {
				if db.Port > 0 && pm.ContainerPort == db.Port {
					g.Edges = append(g.Edges, DependencyEdge{
						From:   "container:" + c.Name,
						To:     "database:" + db.Type,
						Reason: fmt.Sprintf("container exposes port %d matching %s database", db.Port, db.Type),
					})
				}
			}
		}
	}
}

// buildNginxContainerEdges links Nginx vhosts to containers by port.
func (g *DependencyGraph) buildNginxContainerEdges(snapshot *ServerSnapshot) {
	if snapshot.Nginx == nil || snapshot.Docker == nil {
		return
	}

	// Build a map of port → container name
	portToContainer := make(map[int]string)
	for _, c := range snapshot.Docker.Containers {
		for _, pm := range c.Ports {
			if pm.HostPort > 0 {
				portToContainer[pm.HostPort] = c.Name
			}
		}
	}

	// Check proxy_pass directives
	for _, vh := range snapshot.Nginx.VHosts {
		if vh.ProxyPass == "" {
			continue
		}
		port := extractPortFromURL(vh.ProxyPass)
		if port > 0 {
			if containerName, ok := portToContainer[port]; ok {
				g.Edges = append(g.Edges, DependencyEdge{
					From:   "nginx:" + vh.ServerName,
					To:     "container:" + containerName,
					Reason: fmt.Sprintf("proxy_pass to port %d (container %s)", port, containerName),
				})
			}
		}
	}

	// Check upstreams
	for _, upstream := range snapshot.Nginx.Upstreams {
		for _, srv := range upstream.Servers {
			port := extractPortFromAddress(srv.Address)
			if port > 0 {
				if containerName, ok := portToContainer[port]; ok {
					// Find which vhost uses this upstream
					for _, vh := range snapshot.Nginx.VHosts {
						if vh.ProxyPass != "" && strings.Contains(vh.ProxyPass, upstream.Name) {
							g.Edges = append(g.Edges, DependencyEdge{
								From:   "nginx:" + vh.ServerName,
								To:     "container:" + containerName,
								Reason: fmt.Sprintf("upstream %s → port %d (container %s)", upstream.Name, port, containerName),
							})
						}
					}
				}
			}
		}
	}
}

// buildServiceDependencies links systemd services based on After/Requires.
func (g *DependencyGraph) buildServiceDependencies(snapshot *ServerSnapshot) {
	for _, svc := range snapshot.Services {
		for _, dep := range svc.DependsOn {
			// Check if the dependency exists as a node
			if nodeExists(g, "service:"+dep) {
				g.Edges = append(g.Edges, DependencyEdge{
					From:   "service:" + svc.Name,
					To:     "service:" + dep,
					Reason: "systemd After/Requires dependency",
				})
			}
		}
	}
}

// buildServiceDatabaseEdges links systemd services to databases by name convention.
func (g *DependencyGraph) buildServiceDatabaseEdges(snapshot *ServerSnapshot) {
	for _, svc := range snapshot.Services {
		for _, db := range snapshot.Databases {
			// Check if the service name contains the database type
			// e.g., "mysql.service" → MySQL, "postgresql.service" → PostgreSQL
			if strings.Contains(strings.ToLower(svc.Name), db.Type) {
				g.Edges = append(g.Edges, DependencyEdge{
					From:   "service:" + svc.Name,
					To:     "database:" + db.Type,
					Reason: fmt.Sprintf("service name matches %s database", db.Type),
				})
			}
		}
	}
}

// --- Helpers ---

func nodeExists(g *DependencyGraph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// extractPortFromURL extracts the port from a URL like "http://localhost:3000".
func extractPortFromURL(url string) int {
	// Try to find :port in the URL
	parts := strings.Split(url, ":")
	if len(parts) < 3 {
		// No port specified, use default
		if strings.HasPrefix(url, "https") {
			return 443
		}
		return 80
	}
	// Last part should contain the port (possibly with path)
	portPart := parts[len(parts)-1]
	// Remove any path
	if idx := strings.IndexAny(portPart, "/"); idx >= 0 {
		portPart = portPart[:idx]
	}
	port, err := strconv.Atoi(portPart)
	if err != nil {
		return 0
	}
	return port
}

// extractPortFromAddress extracts the port from an address like "127.0.0.1:3000".
func extractPortFromAddress(addr string) int {
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		port, err := strconv.Atoi(addr[idx+1:])
		if err != nil {
			return 0
		}
		return port
	}
	return 0
}

// --- Graph Analysis ---

// TopologicalSort returns the nodes in dependency order.
// Nodes with no dependencies come first, followed by nodes that depend on them.
// Returns an error if there is a cycle.
func (g *DependencyGraph) TopologicalSort() ([]DependencyNode, error) {
	// Build adjacency list and in-degree map
	adj := make(map[string][]string)
	inDegree := make(map[string]int)

	for _, node := range g.Nodes {
		inDegree[node.ID] = 0
	}

	for _, edge := range g.Edges {
		adj[edge.To] = append(adj[edge.To], edge.From)
		inDegree[edge.From]++
	}

	// Kahn's algorithm
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var sorted []string
	for len(queue) > 0 {
		// Take from front
		node := queue[0]
		queue = queue[1:]
		sorted = append(sorted, node)

		for _, dependent := range adj[node] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(sorted) != len(g.Nodes) {
		return nil, fmt.Errorf("dependency cycle detected")
	}

	// Map back to nodes
	nodeMap := make(map[string]DependencyNode)
	for _, n := range g.Nodes {
		nodeMap[n.ID] = n
	}

	var result []DependencyNode
	for _, id := range sorted {
		result = append(result, nodeMap[id])
	}

	return result, nil
}

// --- Cycle Detection ---

// DetectCycles finds all cycles in the dependency graph.
// Returns a list of cycles, where each cycle is a list of node IDs.
func (g *DependencyGraph) DetectCycles() [][]string {
	// Build adjacency list (from → to direction for DFS)
	adj := make(map[string][]string)
	for _, edge := range g.Edges {
		adj[edge.From] = append(adj[edge.From], edge.To)
	}

	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	var path []string

	var dfs func(node string)
	dfs = func(node string) {
		visited[node] = true
		recStack[node] = true
		path = append(path, node)

		for _, next := range adj[node] {
			if !visited[next] {
				dfs(next)
			} else if recStack[next] {
				// Found a cycle — extract it from path
				cycleStart := -1
				for i, id := range path {
					if id == next {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					cycle := make([]string, len(path)-cycleStart)
					copy(cycle, path[cycleStart:])
					cycles = append(cycles, cycle)
				}
			}
		}

		path = path[:len(path)-1]
		recStack[node] = false
	}

	for _, node := range g.Nodes {
		if !visited[node.ID] {
			dfs(node.ID)
		}
	}

	return cycles
}

// HasCycle returns true if the dependency graph contains a cycle.
func (g *DependencyGraph) HasCycle() bool {
	return len(g.DetectCycles()) > 0
}

// --- Dependency Validation ---

// ValidationError represents a problem found during dependency validation.
type ValidationError struct {
	NodeID  string `json:"nodeId"`
	Type    string `json:"type"` // "missing_dependency", "self_dependency", "cycle", "orphan"
	Message string `json:"message"`
}

// Validate checks the dependency graph for common issues.
func (g *DependencyGraph) Validate() []ValidationError {
	var errors []ValidationError

	nodeSet := make(map[string]bool)
	for _, n := range g.Nodes {
		nodeSet[n.ID] = true
	}

	// Check for missing dependencies (edges pointing to non-existent nodes)
	for _, edge := range g.Edges {
		if !nodeSet[edge.To] {
			errors = append(errors, ValidationError{
				NodeID:  edge.From,
				Type:    "missing_dependency",
				Message: fmt.Sprintf("node %q depends on %q which does not exist", edge.From, edge.To),
			})
		}
		if !nodeSet[edge.From] {
			errors = append(errors, ValidationError{
				NodeID:  edge.To,
				Type:    "missing_dependency",
				Message: fmt.Sprintf("edge from %q does not exist as a node", edge.From),
			})
		}
	}

	// Check for self-dependencies
	for _, edge := range g.Edges {
		if edge.From == edge.To {
			errors = append(errors, ValidationError{
				NodeID:  edge.From,
				Type:    "self_dependency",
				Message: fmt.Sprintf("node %q depends on itself", edge.From),
			})
		}
	}

	// Check for cycles
	cycles := g.DetectCycles()
	for _, cycle := range cycles {
		errors = append(errors, ValidationError{
			NodeID:  cycle[0],
			Type:    "cycle",
			Message: fmt.Sprintf("dependency cycle detected: %s", strings.Join(cycle, " → ")),
		})
	}

	return errors
}

// --- Extended Edge Builders ---

// buildEnvVarEdges creates edges based on environment variable connections.
// If a container has DATABASE_URL, REDIS_URL, or AMQP_URL env vars pointing
// to another service, create a dependency edge.
func (g *DependencyGraph) buildEnvVarEdges(snapshot *ServerSnapshot) {
	if snapshot.Docker == nil {
		return
	}

	// Build port → node ID map for resolving env var targets
	portToNode := make(map[int]string)
	for _, node := range g.Nodes {
		if node.Port > 0 {
			portToNode[node.Port] = node.ID
		}
	}

	for _, c := range snapshot.Docker.Containers {
		containerID := "container:" + c.Name
		// Check env var names for database/queue/cache connections
		for _, envName := range c.EnvVarNames {
			var targetPort int
			var reason string

			switch {
			case strings.Contains(strings.ToUpper(envName), "DATABASE") ||
				strings.Contains(strings.ToUpper(envName), "DB_") ||
				strings.Contains(strings.ToUpper(envName), "MYSQL") ||
				strings.Contains(strings.ToUpper(envName), "POSTGRES"):
				// Look for database nodes
				for _, db := range snapshot.Databases {
					if db.Port > 0 {
						targetPort = db.Port
						reason = fmt.Sprintf("env var %s suggests database dependency", envName)
						break
					}
				}
			case strings.Contains(strings.ToUpper(envName), "REDIS") ||
				strings.Contains(strings.ToUpper(envName), "CACHE"):
				// Look for Redis nodes
				for _, db := range snapshot.Databases {
					if db.Type == "redis" && db.Port > 0 {
						targetPort = db.Port
						reason = fmt.Sprintf("env var %s suggests Redis dependency", envName)
						break
					}
				}
			case strings.Contains(strings.ToUpper(envName), "AMQP") ||
				strings.Contains(strings.ToUpper(envName), "RABBIT") ||
				strings.Contains(strings.ToUpper(envName), "QUEUE"):
				// Look for queue nodes
				for _, mq := range snapshot.MessageQueues {
					if mq.Port > 0 {
						targetPort = mq.Port
						reason = fmt.Sprintf("env var %s suggests queue dependency", envName)
						break
					}
				}
			}

			if targetPort > 0 && reason != "" {
				if targetID, ok := portToNode[targetPort]; ok {
					// Avoid duplicate edges
					if !g.hasEdge(containerID, targetID) {
						g.Edges = append(g.Edges, DependencyEdge{
							From:   containerID,
							To:     targetID,
							Reason: reason,
						})
					}
				}
			}
		}
	}
}

// buildVolumeSharingEdges creates edges between containers that share volumes.
func (g *DependencyGraph) buildVolumeSharingEdges(snapshot *ServerSnapshot) {
	if snapshot.Docker == nil {
		return
	}

	// Build volume → container map
	volumeToContainers := make(map[string][]string)
	for _, c := range snapshot.Docker.Containers {
		for _, vol := range c.Volumes {
			volumeToContainers[vol] = append(volumeToContainers[vol], c.Name)
		}
	}

	// Create edges between containers sharing the same volume
	for vol, containers := range volumeToContainers {
		if len(containers) > 1 {
			for i := 0; i < len(containers); i++ {
				for j := i + 1; j < len(containers); j++ {
					a := "container:" + containers[i]
					b := "container:" + containers[j]
					if !g.hasEdge(a, b) && !g.hasEdge(b, a) {
						g.Edges = append(g.Edges, DependencyEdge{
							From:   a,
							To:     b,
							Reason: fmt.Sprintf("shared volume: %s", vol),
						})
					}
				}
			}
		}
	}
}

// hasEdge checks if an edge already exists between two nodes.
func (g *DependencyGraph) hasEdge(from, to string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// MigrationOrder returns nodes in the order they should be migrated.
// This is the reverse of topological sort — dependencies (databases, caches)
// are migrated first, then the services that depend on them.
// Returns an error if there is a cycle.
func (g *DependencyGraph) MigrationOrder() ([]DependencyNode, error) {
	sorted, err := g.TopologicalSort()
	if err != nil {
		return nil, err
	}
	// TopologicalSort returns dependencies first, dependents last.
	// For migration, we want to migrate dependencies first (they come first
	// in topological order), so the order is already correct.
	return sorted, nil
}
