package discovery

import (
	"fmt"
	"strconv"
	"strings"
)

// --- Dependency Graph Types ---

// DependencyNode represents a single node in the dependency graph.
// A node can be a Docker container, a systemd service, a database,
// or an Nginx virtual host.
type DependencyNode struct {
	// ID is a unique identifier for the node.
	ID string `json:"id"`
	// Name is the human-readable name.
	Name string `json:"name"`
	// Type is the node type: "container", "service", "database", "nginx".
	Type string `json:"type"`
	// Port is the port the node listens on (if applicable).
	Port int `json:"port,omitempty"`
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

	// Create nodes for Docker containers
	if snapshot.Docker != nil {
		for _, c := range snapshot.Docker.Containers {
			node := DependencyNode{
				ID:   "container:" + c.Name,
				Name: c.Name,
				Type: "container",
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
			ID:   "database:" + db.Type,
			Name: db.Type,
			Type: "database",
			Port: db.Port,
		})
	}

	// Create nodes for systemd services
	for _, svc := range snapshot.Services {
		g.Nodes = append(g.Nodes, DependencyNode{
			ID:   "service:" + svc.Name,
			Name: svc.Name,
			Type: "service",
		})
	}

	// Create nodes for Nginx vhosts
	if snapshot.Nginx != nil {
		for _, vh := range snapshot.Nginx.VHosts {
			g.Nodes = append(g.Nodes, DependencyNode{
				ID:   "nginx:" + vh.ServerName,
				Name: vh.ServerName,
				Type: "nginx",
			})
		}
	}

	// Build edges
	g.buildContainerDatabaseEdges(snapshot)
	g.buildNginxContainerEdges(snapshot)
	g.buildServiceDependencies(snapshot)
	g.buildServiceDatabaseEdges(snapshot)

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
