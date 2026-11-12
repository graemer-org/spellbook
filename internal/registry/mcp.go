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

// DiscoverMCP reads every JSON file directly in root/mcp. The file name
// without extension is the name of the server.
func DiscoverMCP(root string) ([]MCPServer, []string, error) {
	dir := filepath.Join(root, MCPDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []MCPServer
	var warnings []string
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rel := MCPDir + "/" + e.Name()
		name := strings.TrimSuffix(e.Name(), ".json")
		if !config.ValidName(name) {
			warnings = append(warnings, fmt.Sprintf("skipped %s: %q is not a valid server name", rel, name))
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped %s: it is not a JSON object: %v", rel, err))
			continue
		}
		if _, hasCommand := obj["command"]; !hasCommand {
			if _, hasURL := obj["url"]; !hasURL {
				warnings = append(warnings, fmt.Sprintf("skipped %s: a server needs a \"command\" or a \"url\"", rel))
				continue
			}
		}
		out = append(out, MCPServer{Name: name, Raw: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, warnings, nil
}
