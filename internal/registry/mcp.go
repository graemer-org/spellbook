package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/graemer-org/spellbook/internal/config"
)

// MCPDir is the directory of a registry that holds MCP server definitions.
const MCPDir = "mcp"

// MCPServer is one MCP server definition: a JSON object as the tools
// expect it under "mcpServers", optionally with a "claude" and a "copilot"
// object holding fields for that tool only.
type MCPServer struct {
	Name string
	// Raw is the JSON object as written in the registry.
	Raw json.RawMessage
}

// Hash identifies the definition.
func (m MCPServer) Hash() string {
	sum := sha256.Sum256(m.Raw)
	return hex.EncodeToString(sum[:])
}

// rootMCPFiles are the files in which a repository may list MCP servers
// in the tools' own format: a "mcpServers" object keyed by server name.
var rootMCPFiles = []string{"mcp.json", ".mcp.json"}

// DiscoverMCP reads the MCP servers of a registry: every JSON file directly
// in root/mcp, named after the file, and the servers in a mcp.json or
// .mcp.json at the root. A file in mcp/ wins over a root entry of the same
// name.
func DiscoverMCP(root string) ([]MCPServer, []string, error) {
	var out []MCPServer
	var warnings []string
	seen := map[string]bool{}
	add := func(rel, name string, data []byte) {
		if !config.ValidResourceName(name) {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %q is not a valid server name", rel, name))
			return
		}
		if seen[name] {
			return
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: it is not a JSON object: %v", rel, err))
			return
		}
		if _, hasCommand := obj["command"]; !hasCommand {
			if _, hasURL := obj["url"]; !hasURL {
				warnings = append(warnings, fmt.Sprintf("skipped %s: a server needs a \"command\" or a \"url\"", rel))
				return
			}
		}
		seen[name] = true
		out = append(out, MCPServer{Name: name, Raw: data})
	}

	dir := filepath.Join(root, MCPDir)
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		add(MCPDir+"/"+e.Name(), strings.TrimSuffix(e.Name(), ".json"), data)
	}
	for _, file := range rootMCPFiles {
		data, err := os.ReadFile(filepath.Join(root, file))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		var doc struct {
			Servers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %v", file, err))
			continue
		}
		for name, raw := range doc.Servers {
			add(file+" ("+name+")", name, raw)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	sort.Strings(warnings)
	return out, warnings, nil
}
