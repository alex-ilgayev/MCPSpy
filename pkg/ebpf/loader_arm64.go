//go:build arm64

package ebpf

import _ "embed"

type archObjects = mcpspy_bpfel_arm64Objects

var loadArchObjects = loadMcpspy_bpfel_arm64Objects
