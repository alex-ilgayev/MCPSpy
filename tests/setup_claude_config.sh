#!/bin/bash
# Setup script for Claude Code MCP configuration
# Creates a config.json file with stdio and HTTP MCP servers for testing

set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <config_directory>"
    exit 1
fi

CONFIG_DIR="$1"
CONFIG_FILE="$CONFIG_DIR/config.json"

# Create the configuration file
cat > "$CONFIG_FILE" <<'EOF'
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
    }
  }
}
EOF

# cat > "$CONFIG_FILE" <<'EOF'
# {
#   "mcpServers": {
#     "filesystem": {
#       "command": "npx",
#       "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
#     },
#     "fetch": {
#       "url": "https://127.0.0.1:12346/mcp",
#       "type": "http"
#     }
#   }
# }
# EOF

echo "Created Claude Code config at: $CONFIG_FILE"
cat "$CONFIG_FILE"
